package cli

// wrapperFunction is the §8 shell function, verbatim. A subprocess can't change its
// parent shell's working directory, so `wt go` only prints the target path; this function
// captures it and does the actual `cd`. It works identically in bash and zsh.
const wrapperFunction = `wt() {
  if [[ "$1" == "go" ]]; then
    local target
    target="$(command wt go "${@:2}")" || return $?  # stderr (chatter) passes through
    cd "$target"
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
    COMPREPLY=( $(compgen -W "clone go g list ls status release free shell-init version" -- "${cur}") )
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
  subcommands=(clone go g list ls status release free shell-init version)

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
