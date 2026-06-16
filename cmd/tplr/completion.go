package main

import "fmt"

// flagNames lists every long flag tplr accepts, used to generate static shell
// completion scripts.
var flagNames = []string{
	"-source", "-file", "-dest", "-out", "-out-dir", "-in-place",
	"-env", "-region", "-left", "-right", "-ignore-missing",
	"-dry-run", "-mask", "-validate", "-allow-exec", "-timeout",
	"-retries", "-concurrency", "-version",
}

// runCompletion prints a completion script for the requested shell.
func runCompletion(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: tplr completion <bash|zsh|fish>")
	}
	switch args[0] {
	case "bash":
		fmt.Print(bashCompletion())
	case "zsh":
		fmt.Print(zshCompletion())
	case "fish":
		fmt.Print(fishCompletion())
	default:
		return fmt.Errorf("unsupported shell %q (supported: bash, zsh, fish)", args[0])
	}
	return nil
}

func joinFlags(sep string) string {
	out := ""
	for i, f := range flagNames {
		if i > 0 {
			out += sep
		}
		out += f
	}
	return out
}

func bashCompletion() string {
	return fmt.Sprintf(`# bash completion for tplr
# install: tplr completion bash > /etc/bash_completion.d/tplr
_tplr() {
    local cur prev
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    if [[ ${COMP_CWORD} -eq 1 ]]; then
        COMPREPLY=( $(compgen -W "completion %s" -- "${cur}") )
        return 0
    fi
    case "${prev}" in
        -source|-file|-dest|-out|-out-dir)
            COMPREPLY=( $(compgen -f -- "${cur}") )
            return 0
            ;;
    esac
    COMPREPLY=( $(compgen -W "%s" -- "${cur}") )
}
complete -F _tplr tplr
`, joinFlags(" "), joinFlags(" "))
}

func zshCompletion() string {
	return fmt.Sprintf(`#compdef tplr
# zsh completion for tplr
# install: tplr completion zsh > "${fpath[1]}/_tplr"
_tplr() {
    local -a flags
    flags=(%s)
    _arguments '1: :->cmd' '*: :->args'
    case $state in
        cmd)
            compadd completion ${flags}
            ;;
        *)
            compadd ${flags}
            _files
            ;;
    esac
}
_tplr "$@"
`, quoteEachFlag())
}

func quoteEachFlag() string {
	out := ""
	for i, f := range flagNames {
		if i > 0 {
			out += " "
		}
		out += "'" + f + "'"
	}
	return out
}

func fishCompletion() string {
	out := "# fish completion for tplr\n# install: tplr completion fish > ~/.config/fish/completions/tplr.fish\n"
	for _, f := range flagNames {
		out += fmt.Sprintf("complete -c tplr -l %s\n", f[1:])
	}
	out += "complete -c tplr -n '__fish_use_subcommand' -a completion\n"
	return out
}
