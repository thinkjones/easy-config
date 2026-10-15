package migrate

import (
	"regexp"
	"strings"
)

// oldShim is where the old tool's shim was installed; a mise.toml sources it in [env].
const oldShim = "{{ env.HOME }}/.local/share/fsconfig/env.sh"

var oldCommand = regexp.MustCompile(`\bfsconfig (codegen shell|codegen python|manifest|doctor|env)\b`)

var newCommand = map[string]string{
	"codegen shell":  "ezc env export",
	"codegen python": "ezc codegen python",
	"manifest":       "ezc manifest",
	"doctor":         "ezc doctor",
	"env":            "ezc env",
}

// rewriteMise rewrites a mise.toml for ezc: the shim's path, the old tool's commands and the .fs-config/ folder.
// Anything else that names the old tool is left for the report.
func rewriteMise(text string) string {
	text = strings.ReplaceAll(text, oldShim, ".ezc/env.sh")
	text = oldCommand.ReplaceAllStringFunc(text, func(m string) string {
		return newCommand[strings.TrimPrefix(m, "fsconfig ")]
	})
	return strings.ReplaceAll(text, ".fs-config/", ".ezc/")
}
