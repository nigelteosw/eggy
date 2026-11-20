# Eggy: what every login shell in a terminal or run starts with.
#
# /etc/profile resets PATH from scratch, which drops the image's Node and Go
# and the account's own installs. Put them back: the image's tools first, the
# account's ~/.local/bin last, so nothing installed there replaces a system
# program unless the owner says so in their own shell config.
for d in /usr/local/node/bin /usr/local/go/bin; do
  case ":$PATH:" in *":$d:"*) ;; *) PATH="$d:$PATH" ;; esac
done
if [ -n "${HOME-}" ]; then
  case ":$PATH:" in *":$HOME/.local/bin:"*) ;; *) PATH="$PATH:$HOME/.local/bin" ;; esac
fi
export PATH

# A login bash reads ~/.profile, not ~/.bashrc, and a new account has neither
# ~/.profile nor ~/.bash_profile, so what installers append to ~/.bashrc
# would never apply. Read it, unless the owner has a profile of their own
# (which then decides, as on any Debian machine).
if [ -n "${BASH_VERSION-}" ] && [ -n "${HOME-}" ] && [ -f "$HOME/.bashrc" ] \
  && [ ! -f "$HOME/.bash_profile" ] && [ ! -f "$HOME/.bash_login" ] && [ ! -f "$HOME/.profile" ]; then
  . "$HOME/.bashrc"
fi
