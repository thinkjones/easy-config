// Package proc is how ezc runs another program and waits for it.
package proc

// Command is a program ezc runs and waits for.
type Command struct {
	Dir   string   // working directory; "" is the process's
	Env   []string // the whole environment as K=V; nil is the process's
	Stdin []byte   // fed to the program; ignored when Interactive
	Name  string
	Args  []string
	// Interactive wires the program to the terminal (an editor); Stdout and Stderr are then not captured.
	Interactive bool
}

// Runner runs a Command. err is non-nil when it could not start or exited non-zero.
type Runner func(Command) (stdout, stderr []byte, err error)
