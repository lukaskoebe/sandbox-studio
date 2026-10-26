# Sandbox Studio's defaults for zsh, sourced at the end of /etc/zsh/zshrc, so ~/.zshrc runs
# after them and can change any of them.
HISTFILE=~/.zsh_history
HISTSIZE=50000
SAVEHIST=50000
setopt share_history hist_ignore_dups hist_ignore_space extended_history interactive_comments
bindkey -e
autoload -Uz compinit
mkdir -p ~/.cache && compinit -d ~/.cache/zcompdump
zstyle ':completion:*' menu select
zstyle ':completion:*' matcher-list 'm:{a-z}={A-Z}'
PROMPT='%F{green}%n@%m%f:%F{blue}%~%f%# '
# Per-directory toolchains, as rootfs/etc/profile.d/studio-mise.sh does for bash.
if [[ $EUID -ne 0 && -x /usr/local/bin/mise ]]; then
    eval "$(/usr/local/bin/mise activate zsh)"
fi
