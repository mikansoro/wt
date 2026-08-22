package cli

// wrapperFunction is the §8 shell function. A subprocess can't change its parent shell's
// working directory, so `wt go` only prints the target path; this function captures it
// and does the actual `cd`. Captured output that isn't a directory (cobra intercepts
// --help/-h before RunE and prints usage on stdout, exit 0) is echoed instead of handed
// to cd. It works identically in bash and zsh, and also matches the `g` alias that the
// completion scripts advertise.
const wrapperFunction = `wt() {
  if [[ "$1" == "go" || "$1" == "g" ]]; then
    local target
    target="$(command wt "$@")" || return $?  # stderr (chatter) passes through
    if [[ -d "$target" ]]; then
      cd "$target"
    elif [[ -n "$target" ]]; then
      printf '%s\n' "$target"  # not a path: --help/usage text
    fi
  else
    command wt "$@"
  fi
}
`

// wtBranchListCmd is the pipeline used by both completion scripts to enumerate branch
// names, run through git so it works from anywhere inside the repo (see agent-plan.md §8).
const wtBranchListCmd = `git for-each-ref --format='%(refname:short)' refs/heads refs/remotes 2>/dev/null | sed 's|^origin/||' | sort -u`

const bashCompletion = `_wt() {
  local cur cmd
  cur="${COMP_WORDS[COMP_CWORD]}"
  cmd="${COMP_WORDS[1]}"

  if [[ ${COMP_CWORD} -eq 1 ]]; then
    COMPREPLY=( $(compgen -W "adopt clone go g list ls status release free shell-init version" -- "${cur}") )
    return 0
  fi

  case "${cmd}" in
    go|g)
      COMPREPLY=( $(compgen -W "$(_wt_branches)" -- "${cur}") )
      ;;
    release|free)
      COMPREPLY=( $(compgen -W "$(_wt_branches) main slot-1 slot-2 slot-3 slot-4 slot-5 slot-6" -- "${cur}") )
      ;;
    *)
      COMPREPLY=()
      ;;
  esac
}

_wt_branches() {
  ` + wtBranchListCmd + `
}

complete -F _wt wt
`

const zshCompletion = `_wt() {
  local -a subcommands
  subcommands=(adopt clone go g list ls status release free shell-init version)

  if (( CURRENT == 2 )); then
    compadd -a subcommands
    return
  fi

  case "${words[2]}" in
    go|g)
      compadd -- $(_wt_branches)
      ;;
    release|free)
      compadd -- $(_wt_branches) main slot-1 slot-2 slot-3 slot-4 slot-5 slot-6
      ;;
  esac
}

_wt_branches() {
  ` + wtBranchListCmd + `
}

compdef _wt wt
`
