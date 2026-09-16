package main

// shellInit is the wrapper function that makes switching possible.
//
// cx runs as a separate process, so nothing it exports can reach the shell that
// started it. The wrapper gives cx a scratch file to write environment changes
// into, then sources that file itself -- sourcing runs in the caller's shell,
// and so can change it.
//
// It is valid in both zsh and bash.
//
// The autoPinPlaceholder token is replaced with the auto-pin block, which is
// computed when shell-init runs rather than on every prompt. Token substitution
// is used rather than fmt.Sprintf because the script contains zsh colour codes
// such as %F{242} that a format string would try to interpret.
const shellInit = `# cx shell integration
cx() {
  local _cx_out _cx_rc
  _cx_out="$(mktemp -t cx.XXXXXX)" || return 1
  CX_SHELL_OUT="$_cx_out" command cx "$@"
  _cx_rc=$?
  if [ -s "$_cx_out" ]; then
    . "$_cx_out"
  fi
  rm -f "$_cx_out"
  return $_cx_rc
}

# Prompt segment. Reads only local files, so it never stalls a prompt on a slow
# network. A trailing "!" means this shell's gcloud target comes from
# machine-global state and another terminal can change it.
cx_prompt() { command cx prompt 2>/dev/null; }

# Starship owns the prompt and rewrites RPROMPT on every render, so touching it
# here would be pointless. Starship's own aws and gcloud modules already show
# the current target; add cx as a custom module for the warnings it cannot see:
#
#   [custom.cx]
#   command = "cx prompt --warn"
#   when = "cx prompt --warn"
#   format = "[$symbol$output]($style) "
#   symbol = "⚠ "
#   style = "bold red"
#
# Do not set a "shell" key there: starship pipes the command to the shell on
# stdin, so an explicit ["sh", "-c"] becomes "sh -c -c" and fails silently.
#
# then add ${custom.cx} to your starship format string.
if [ -n "$ZSH_VERSION" ] && [ -z "$CX_NO_RPROMPT" ] && [ -z "$STARSHIP_SHELL" ]; then
  setopt PROMPT_SUBST 2>/dev/null
  RPROMPT='%F{242}$(cx_prompt)%f'"$RPROMPT"
fi
__CX_AUTOPIN__`

// autoPinTemplate pins each new shell to whatever gcloud configuration was
// selected at the moment the shell opened.
//
// Without it, every new terminal falls back to the machine-wide active_config
// file, so another terminal can retarget it at any time -- and a warning about
// that would be permanently true and therefore useless. Pinning at startup
// removes the hazard instead of reporting it forever.
//
// The := form assigns only when the variable is unset or empty, so an inherited
// value (a subshell, or an explicit cx use) always wins.
const autoPinTemplate = `
# Pin this shell to the gcloud configuration that was active when it opened, so
# another terminal switching cannot retarget it. Set CX_NO_AUTOPIN=1 to opt out,
# or run 'cx clear gcp' to follow the machine-wide setting again.
# A plain assignment, not "${VAR:=...}": inside double quotes the quoting around
# the value would be taken literally, pinning the shell to a name that includes
# the quote characters and does not exist.
if [ -z "$CX_NO_AUTOPIN" ] && [ -z "$CLOUDSDK_ACTIVE_CONFIG_NAME" ]; then
  export CLOUDSDK_ACTIVE_CONFIG_NAME=__CX_CONFIG__
fi
`

// autoPinPlaceholder marks where the auto-pin block is spliced into shellInit.
const autoPinPlaceholder = "__CX_AUTOPIN__"

// configPlaceholder marks where the pinned configuration name goes.
const configPlaceholder = "__CX_CONFIG__"
