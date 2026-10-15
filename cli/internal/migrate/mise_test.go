package migrate

import "testing"

func TestRewriteMise(t *testing.T) {
	in := `[env]
_.source = "{{ env.HOME }}/.local/share/fsconfig/env.sh"

[tasks."env:check"]
run = "fsconfig env"

[tasks.gen]
run = "fsconfig codegen python -m .fs-config/manifest.yaml -o src/x/env.py"
run2 = 'eval "$(fsconfig codegen shell)"'
run3 = "fsconfig doctor || status=$?"
run4 = "fsconfig manifest validate"
run5 = "fsconfig envelope"
# fsconfig composes the store
`
	want := `[env]
_.source = ".ezc/env.sh"

[tasks."env:check"]
run = "ezc env"

[tasks.gen]
run = "ezc codegen python -m .ezc/manifest.yaml -o src/x/env.py"
run2 = 'eval "$(ezc env export)"'
run3 = "ezc doctor || status=$?"
run4 = "ezc manifest validate"
run5 = "fsconfig envelope"
# fsconfig composes the store
`
	if got := rewriteMise(in); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}
