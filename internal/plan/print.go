package plan

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

// Print renders the resolved plan as stable, aligned key/value lines followed
// by the exact `msb run` argv. It is the human output for `plan` and for
// `run --verbose`.
func Print(w io.Writer, p *RuntimePlan) {
	f := func(key string, value any) {
		fmt.Fprintf(w, "%-16s %s\n", key+":", value)
	}
	f("agent", p.AgentName)
	f("image", p.Image)
	f("workspace", p.WorkspaceHost)
	f("guest workspace", p.WorkspaceGuest)
	f("home volume", p.HomeMount)
	if p.SharedState != nil {
		f("shared state", p.SharedState.Mount)
	}
	var resources []string
	if p.Resources.CPUs > 0 {
		resources = append(resources, fmt.Sprintf("cpus=%d", p.Resources.CPUs))
	}
	if p.Resources.Memory != "" {
		resources = append(resources, fmt.Sprintf("memory=%s", p.Resources.Memory))
	}
	resources = append(resources, fmt.Sprintf("root-disk=%s", p.Resources.RootDisk))
	f("resources", strings.Join(resources, " "))
	if p.Security != "" {
		f("security", p.Security)
	}
	f("user", p.User)
	switch {
	case p.Network.Public:
		f("network", "public")
	case p.Network.NoNet:
		f("network", "no-net")
	}
	for _, rule := range p.Network.Rules {
		f("  rule", rule)
	}
	if len(p.Environment) > 0 {
		f("environment", strings.Join(p.Environment, " "))
	}
	var command []string
	if len(p.Environment) > 0 {
		command = append(command, "env")
	}
	command = append(command, p.Environment...)
	command = append(command, p.Command...)
	command = append(command, p.AgentArgs...)
	f("command", shellJoin(command))

	fmt.Fprintln(w)
	fmt.Fprintln(w, "msb run argv:")
	fmt.Fprintln(w, "  "+shellJoin(p.MsbArgv()))
}

// shellJoin renders args for display, quoting only those that need it.
func shellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, a := range args {
		if needsQuoting(a) {
			quoted = append(quoted, shellQuote(a))
		} else {
			quoted = append(quoted, a)
		}
	}
	return strings.Join(quoted, " ")
}

// shellQuote renders a single argument with POSIX single-quote escaping.
func shellQuote(s string) string {
	var b bytes.Buffer
	b.WriteByte('\'')
	for _, r := range s {
		if r == '\'' {
			b.WriteString(`'\''`)
		} else {
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

func needsQuoting(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '.' || r == '-' || r == '_' || r == '/' || r == ':' || r == ',' || r == '=' ||
			r == '@' || r == '*' || r == '#' || r == '(' || r == ')' || r == ';' || r == '|' ||
			r == '&' || r == '~') {
			return true
		}
	}
	return false
}
