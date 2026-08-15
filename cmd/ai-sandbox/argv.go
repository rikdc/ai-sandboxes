package main

import "strings"

// mcpLoginValuedFlags names the value-taking flags accepted by the
// codex/claude mcp-login subcommands. Both hyphen and double-hyphen
// forms are listed because Go's flag package accepts both.
var mcpLoginValuedFlags = map[string]bool{
	"--timeout":       true,
	"-timeout":        true,
	"--callback-port": true,
	"-callback-port":  true,
}

// reorderFlagsFirst splits args into flag args and positional args and
// returns them concatenated flags-first, so a caller can then pass the
// result to flag.FlagSet.Parse — which by design stops at the first
// positional and would otherwise refuse `... <server-name> --timeout 2s`.
//
// valuedFlags names the flags that consume the next argv element (e.g.
// "--timeout"). Bool-style flags need not be listed. `-flag=value` and
// `--flag=value` forms are always recognised as single-element flags.
// Everything after `--` is treated as positional and preserved verbatim.
//
// This is deliberately narrow: it exists so the auth subcommands can
// accept `codex mcp login notion --timeout 2s` alongside the
// `--timeout 2s notion` form. Consumers that need real POSIX-style
// intermixed parsing should reach for a dedicated flag library instead.
func reorderFlagsFirst(args []string, valuedFlags map[string]bool) []string {
	var flags, positional []string
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" {
			// Preserve the separator so flag.Parse also stops here and does
			// not try to interpret a following `-foo` as an unknown flag.
			positional = append(positional, args[i:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			// A `--flag=value` form carries its value inline; a `-flag value`
			// form needs the next element if the flag is known-valued.
			if !strings.Contains(a, "=") && valuedFlags[a] {
				if i+1 < len(args) {
					flags = append(flags, args[i+1])
					i++
				}
			}
			i++
			continue
		}
		positional = append(positional, a)
		i++
	}
	return append(flags, positional...)
}
