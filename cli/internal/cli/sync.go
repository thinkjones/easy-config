package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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

	requestsDir = "requests"
)

// storeSync is what a command learns about the store before it uses it.
type storeSync struct {
	Git      bool   // the store is a git repo
	Dir      string // the store's folder, when it is one
	Status   gitstore.Status
	Err      error     // why Status could not be read; the store's state is then not known
	Fetched  time.Time // the last fetch that worked; zero when never
	Offline  bool      // a fetch was tried just now and failed
	Requests []string  // names in the store's requests folder, sorted
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
	if remote, err := g.Remote(dir); err == nil && remote != "" {
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
	s.Requests = requests(p.StoreEzc())
	return s
}

// requests are the names of the access requests in a store's .ezc folder: its requests/*.yaml, sorted.
// Whoever can push to the store names those files, so a name that could not be a recipient's is left out.
func requests(storeEzc string) []string {
	entries, err := os.ReadDir(filepath.Join(storeEzc, requestsDir))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".yaml"); ok && recipients.ValidName(name) && !e.IsDir() {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
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
		pending := count(n, "access request") + " pending: " + strings.Join(s.Requests, ", ")
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
