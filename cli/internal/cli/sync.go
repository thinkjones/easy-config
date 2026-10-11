package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/thinkjones/easy-config/cli/internal/config"
	"github.com/thinkjones/easy-config/cli/internal/gitstore"
	"github.com/thinkjones/easy-config/cli/internal/paths"
	"github.com/thinkjones/easy-config/cli/internal/recipients"
)

const (
	// syncMaxAge is how long a fetch attempt stands before a command tries again.
	syncMaxAge = 10 * time.Minute
	// syncTimeout is how long a command waits for that fetch.
	syncTimeout = 3 * time.Second
)

// storeSync is what a command learns about the store before it uses it.
type storeSync struct {
	Git      bool   // the store is a git repo
	Dir      string // the store's folder, when it is one
	Status   gitstore.Status
	Err      error     // why Status could not be read; the store's state is then not known
	Fetched  time.Time // the last fetch that worked; zero when never
	Offline  bool      // a fetch was tried just now and failed
	Requests []string  // names of the pending access requests: on a branch of origin or in the store's requests folder, sorted
	Due      int       // how many entries rotation-due.yaml holds
}

// storeGit is git as this machine runs it.
func storeGit(sys System) gitstore.Git {
	return gitstore.Git{Run: sys.Exec, Environ: sys.Environ()}
}

// loadConfig is the machine's config; the zero Config when there is none.
func loadConfig(sys System) (config.Config, error) {
	p, err := paths.Resolve(sys.Getenv, sys.GOOS)
	if err != nil {
		return config.Config{}, err
	}
	c, _, err := config.Load(p.Config())
	return c, err
}

// syncStore fetches when the store has a remote and the last attempt is at least maxAge old, then reads the status.
// It never fails a command: a store that is a git repo whose status cannot be read has Err set, which stops a write and no read.
func syncStore(sys System, maxAge, timeout time.Duration) storeSync {
	p, err := paths.Resolve(sys.Getenv, sys.GOOS)
	if err != nil {
		return storeSync{}
	}
	g, dir := storeGit(sys), p.Store()
	repo, err := g.IsRepo(dir)
	if !repo {
		return storeSync{}
	}
	s := storeSync{Git: true, Dir: dir, Err: err}
	if err != nil {
		return s
	}
	state := readState(p.State())
	s.Fetched = stateTime(state, "fetched")
	remote, err := g.Remote(dir)
	if err == nil && remote != "" {
		now := sys.Now()
		// An attempt dated after now is a clock that moved; it says nothing.
		if age := now.Sub(stateTime(state, "attempted")); age < 0 || age >= maxAge {
			setStateTime(state, "attempted", now)
			if err := g.Fetch(dir, timeout); err == nil {
				s.Fetched = now
				setStateTime(state, "fetched", now)
			} else {
				s.Offline = errors.Is(err, gitstore.ErrUnreachable)
			}
			writeState(p.State(), state)
		}
	}
	s.Status, s.Err = g.Status(dir)
	s.Requests = requests(sys, dir, err == nil && remote != "")
	s.Due = len(dueEntries(p.StoreEzc()))
	return s
}

// dueEntries is rotation-due.yaml in a store's .ezc folder; an unreadable file holds none, which is for the commands that read it to say.
func dueEntries(storeEzc string) recipients.Due {
	due, err := recipients.LoadDue(storeEzc)
	if err != nil {
		return nil
	}
	return due
}

// requestNames are the names somebody asks for access to the store in storeDir under: those of the request branches
// of origin as of the last fetch (asked of git only when remote is true: it is the one git call), and those of the
// files in the store's requests folder. Whoever can push to the store names both, and the names go to the terminal,
// so a name that could not be a recipient's is left out. A failure is no names.
func requestNames(sys System, storeDir string, remote bool) (branches, files []string) {
	if remote {
		refs, _ := storeGit(sys).RemoteBranches(storeDir, requestBranch)
		for _, ref := range refs {
			if name := strings.TrimPrefix(ref, requestBranch); recipients.ValidName(name) {
				branches = append(branches, name)
			}
		}
	}
	entries, _ := os.ReadDir(filepath.Join(storeDir, ezcDir, recipients.RequestsDir))
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".yaml"); ok && recipients.ValidName(name) && !e.IsDir() {
			files = append(files, name)
		}
	}
	return branches, files
}

// requests are the names of the pending access requests of the store in storeDir, sorted: pendingRequests' names,
// without reading a request.
func requests(sys System, storeDir string, remote bool) []string {
	return sortedUnion(requestNames(sys, storeDir, remote))
}

// sortedUnion is every name of a and b once, sorted.
func sortedUnion(a, b []string) []string {
	names := slices.Concat(a, b)
	slices.Sort(names)
	return slices.Compact(names)
}

// pending is one request for access to the store, as whoever filed it wrote it: nothing in it is trusted.
type pending struct {
	Name   string
	Req    recipients.Request
	Author string // the Git author of the commit that filed it; "" when git knows none
	Branch bool   // on a request branch of origin
	Commit string // the commit that branch was at when the request was read; "" when it could not be told
	File   bool   // in the store's working tree
	Err    error  // why it is not a valid request
}

// pendingRequests are the requests for access to the store in storeDir: the union, by name and sorted, of the request
// branches of origin as of the last fetch and of the files in the working tree's requests folder. Of a branch only
// its request file is read, at the commit the branch is at when this is called (pending.Commit), and origin is asked
// only when remote is true: a store that is no git repo, or has no remote, has no branch. A name that fails ValidName is left out entirely; a request that cannot be read or fails
// ParseRequest, or that its branch and the working tree hold differently, is kept with Err.
func pendingRequests(sys System, storeDir string, remote bool) []pending {
	g, valid := storeGit(sys), backendFor(sys).ValidRecipient
	branches, files := requestNames(sys, storeDir, remote)
	// The author of a request that is only in the working tree is asked of origin's copy of the store's branch.
	branch := ""
	var all []pending
	for _, name := range sortedUnion(branches, files) {
		q := pending{Name: name, Branch: slices.Contains(branches, name), File: slices.Contains(files, name)}
		rel := ezcDir + "/" + recipients.RequestPath(name)
		if q.Branch {
			// Everything is read at one commit, which is also the one a later delete of the branch is held to.
			var err error
			var data []byte
			if q.Commit, err = g.RemoteCommit(storeDir, requestBranch+name); err == nil {
				data, err = g.ShowAt(storeDir, q.Commit, rel)
			}
			switch {
			case errors.Is(err, fs.ErrNotExist):
				q.Err = errors.New("its branch holds no request")
			case err != nil:
				q.Err = errors.New("it cannot be read from its branch")
			default:
				q.Req, q.Err = recipients.ParseRequest(name, data, valid)
				q.Author, _ = g.LastAuthorAt(storeDir, q.Commit, rel)
			}
		}
		if q.File {
			path := filepath.Join(storeDir, filepath.FromSlash(rel))
			var req recipients.Request
			var err error
			// Only a plain file is read: the store's history can hold a link to anything.
			if info, lerr := os.Lstat(path); lerr != nil || !info.Mode().IsRegular() {
				err = errors.New("it is not a plain file")
			} else if data, rerr := os.ReadFile(path); rerr != nil {
				err = errors.New("it cannot be read")
			} else {
				req, err = recipients.ParseRequest(name, data, valid)
			}
			switch {
			case !q.Branch:
				q.Req, q.Err = req, err
				if branch == "" && remote {
					st, _ := g.Status(storeDir)
					branch = st.Branch
				}
				if branch != "" {
					q.Author, _ = g.LastAuthor(storeDir, branch, rel)
				}
			case q.Err == nil && err != nil:
				q.Err = err
			case q.Err == nil && req != q.Req:
				q.Err = errors.New("its branch and the store hold different requests")
			}
		}
		if q.Err != nil {
			q.Req = recipients.Request{}
		}
		all = append(all, q)
	}
	return all
}

// readState is state.json as its keys; a missing or unreadable file is no keys.
func readState(path string) map[string]json.RawMessage {
	state := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if err != nil {
		return state
	}
	if err := json.Unmarshal(data, &state); err != nil || state == nil {
		return map[string]json.RawMessage{}
	}
	return state
}

// stateTime is the time state holds under key; zero when it holds none.
func stateTime(state map[string]json.RawMessage, key string) time.Time {
	var text string
	if json.Unmarshal(state[key], &text) != nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, text)
	return t
}

func setStateTime(state map[string]json.RawMessage, key string, t time.Time) {
	state[key], _ = json.Marshal(t.UTC().Format(time.RFC3339))
}

// writeState replaces state.json. A failure is not reported: the next command fetches again.
func writeState(path string, state map[string]json.RawMessage) {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil || os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(append(data, '\n'))
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		os.Remove(tmp.Name())
	}
}

// cannotTell is the refusal for a store whose state git could not give.
func cannotTell(dir string, err error) error {
	switch {
	case errors.Is(err, gitstore.ErrNoBranch):
		return fmt.Errorf("the store in %s is not on a branch (a rebase may be in progress). Finish or abort it with git, then run this again.", dir)
	case errors.Is(err, gitstore.ErrNotInstalled):
		return errors.New("git is not installed, so a change to the store could not be committed. Install git, then run this again.")
	}
	return fmt.Errorf("cannot tell how the store in %s stands: %s.", dir, gitReason(err))
}

// gitReason is a gitstore error as part of a sentence: without its "git: " and its full stop.
func gitReason(err error) string {
	return strings.TrimSuffix(strings.TrimPrefix(err.Error(), "git: "), ".")
}

// indicate prints the one line to stderr when there is something to say.
func indicate(sys System, s storeSync) {
	if !s.Git {
		return
	}
	const red, amber, reset = "\x1b[31m", "\x1b[33m", "\x1b[0m"
	say := func(colour, line string) {
		if sys.StderrIsTerminal() && sys.Getenv("NO_COLOR") == "" {
			line = colour + line + reset
		}
		fmt.Fprintln(sys.Stderr, line)
	}
	if s.Err != nil {
		say(red, fmt.Sprintf("store: cannot tell how the store stands in %s (%s)", s.Dir, gitReason(s.Err)))
		return
	}

	line, colour := "", red
	switch st := s.Status; st.State() {
	case gitstore.Ahead:
		line, colour = count(st.Ahead, "commit")+" ahead of origin — run ezc store push", amber
	case gitstore.Behind:
		line = count(st.Behind, "commit") + " behind origin — run ezc store pull"
	case gitstore.Diverged:
		line = fmt.Sprintf("diverged from origin (%d ahead, %d behind) — run ezc store pull", st.Ahead, st.Behind)
	case gitstore.Uncommitted:
		line = "uncommitted changes in " + s.Dir + alsoStands(st)
	}
	if n := len(s.Requests); n > 0 {
		pending := count(n, "access request") + " pending: " + someNames(s.Requests)
		if line == "" {
			line, colour = pending, amber
		} else {
			line += " · " + pending
		}
	}
	if line != "" {
		say(colour, "store: "+line)
	}
	if s.Offline {
		checked := "never checked"
		if !s.Fetched.IsZero() {
			checked = "last checked " + ago(sys.Now().Sub(s.Fetched))
		}
		say(amber, "store: origin could not be reached; "+checked)
	}
}

// someNames is the first names of a list and how many more there are: whoever can push to the store can file any
// number of requests, and the line is printed by every command.
func someNames(names []string) string {
	const shown = 5
	if len(names) <= shown {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:shown], ", "), len(names)-shown)
}

// alsoStands is what follows "uncommitted changes" when the store is also ahead, behind or diverged; "" when it is level.
func alsoStands(st gitstore.Status) string {
	switch {
	case st.Ahead > 0 && st.Behind > 0:
		return fmt.Sprintf("; diverged from origin (%d ahead, %d behind)", st.Ahead, st.Behind)
	case st.Behind > 0:
		return "; " + count(st.Behind, "commit") + " behind origin"
	case st.Ahead > 0:
		return "; " + count(st.Ahead, "commit") + " ahead of origin"
	}
	return ""
}

// count is "1 commit" or "2 commits".
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// ago is how long ago d was, in the largest whole unit: just now, minutes, hours or days.
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return count(int(d/time.Minute), "minute") + " ago"
	case d < 24*time.Hour:
		return count(int(d/time.Hour), "hour") + " ago"
	}
	return count(int(d/(24*time.Hour)), "day") + " ago"
}

// touchStore is syncStore with the 10 minute rule followed by indicate. Commands that read or write the store call it once.
func touchStore(sys System) storeSync {
	s := syncStore(sys, syncMaxAge, syncTimeout)
	indicate(sys, s)
	return s
}

// guardStoreWrite runs touchStore and refuses the write when the store is behind or diverged, or when that cannot be told.
func guardStoreWrite(sys System) (storeSync, error) {
	s := touchStore(sys)
	if s.Git && s.Err != nil {
		return s, cannotTell(s.Dir, s.Err)
	}
	const pull = "Run `ezc store pull` first: a change made now could not be merged."
	// The counts, not the state: uncommitted changes do not make a store less behind.
	switch st := s.Status; {
	case st.Behind > 0 && st.Ahead > 0:
		return s, fmt.Errorf("the store has diverged from origin. %s", pull)
	case st.Behind > 0:
		return s, fmt.Errorf("the store is %s behind origin. %s", count(st.Behind, "commit"), pull)
	}
	return s, nil
}

// guardStoreFiles is an error when one of the named files of the store's .ezc (base names) has uncommitted changes.
// A commit takes a file whole, so a write to such a file would put what was changed by hand under its own message.
func guardStoreFiles(sys System, s storeSync, files ...string) error {
	if !s.Git || !s.Status.Dirty {
		return nil
	}
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = ezcDir + "/" + file
	}
	changed, err := storeGit(sys).Changed(s.Dir, paths...)
	if err != nil {
		return err
	}
	for i, path := range paths {
		if slices.Contains(changed, path) {
			return fmt.Errorf("%s has uncommitted changes in %s. Commit or discard them with git, then run this again.", files[i], s.Dir)
		}
	}
	return nil
}

// commitStore commits the named files of the store's .ezc (base names) with message. It does nothing for a store that is not a git repo.
// Only those files are committed: whatever else was changed in the store by hand stays uncommitted.
func commitStore(sys System, s storeSync, message string, files ...string) error {
	if !s.Git {
		return nil
	}
	author := gitstore.Author{Name: "ezc"}
	if c, err := loadConfig(sys); err == nil && c.User != "" {
		author.Name = c.User
	}
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = ezcDir + "/" + file
	}
	if _, err := storeGit(sys).Commit(s.Dir, message, author, paths...); err != nil {
		return fmt.Errorf("the change was written but could not be committed: %s. Commit it in %s with git.", strings.TrimPrefix(err.Error(), "git: "), s.Dir)
	}
	return nil
}

// fileState is a file's bytes, and whether it was there.
type fileState struct {
	found bool
	data  []byte
}

// fileStates is each of the named files in dir as it is now.
func fileStates(dir string, names []string) []fileState {
	states := make([]fileState, len(names))
	for i, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		states[i] = fileState{err == nil, data}
	}
	return states
}

// changedFiles are those of names whose file in dir is not as before says, in the order given.
func changedFiles(dir string, names []string, before []fileState) []string {
	var changed []string
	for i, now := range fileStates(dir, names) {
		if now.found != before[i].found || !bytes.Equal(now.data, before[i].data) {
			changed = append(changed, names[i])
		}
	}
	return changed
}
