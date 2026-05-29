{
  description = "ccgate - Claude Code PermissionRequest hook (Go)";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = f:
        nixpkgs.lib.genAttrs systems (system: f {
          inherit system;
          pkgs = import nixpkgs { inherit system; };
        });
    in
    {
      devShells = forAllSystems ({ pkgs, ... }: {
        default = pkgs.mkShell {
          name = "ccgate";

          packages = with pkgs; [
            go_1_25
            gopls
            gotools
            go-tools
            golangci-lint
            delve
            git
          ];

          shellHook = ''
            export GOPATH="''${GOPATH:-$HOME/go}"
            export PATH="$GOPATH/bin:$PATH"

            # Append host PATH so outside-the-shell tools (mise, gh, brew, etc.) stay reachable.
            # Nix paths keep priority; host dirs are fallback only.
            for _p in \
              "$HOME/.local/share/mise/shims" \
              "$HOME/.local/bin" \
              "$HOME/.nix-profile/bin" \
              "/nix/var/nix/profiles/default/bin" \
              "/etc/profiles/per-user/$USER/bin" \
              "/opt/homebrew/bin" \
              "/opt/homebrew/sbin" \
              "/usr/local/bin" \
              "/usr/local/sbin" \
              "/usr/bin" \
              "/bin" \
              "/usr/sbin" \
              "/sbin"; do
              if [ -d "$_p" ] && [[ ":$PATH:" != *":$_p:"* ]]; then
                PATH="$PATH:$_p"
              fi
            done
            unset _p
            export PATH

            echo "ccgate dev shell"
            echo "  $(go version)"
            echo "  $(golangci-lint --version 2>/dev/null | head -1)"

            # nix develop forces bash and overwrites $SHELL with the nix store bash,
            # so personal aliases/functions in ~/.zshrc are not loaded. Look up the
            # user's actual login shell from the OS and re-exec into it.
            # Only run when interactive and not already re-exec'd this session.
            if [ -z "$CCGATE_DEVSHELL_EXEC" ] && [ -t 0 ] && [ -t 1 ]; then
              _login_shell=""
              if [ "$(uname -s)" = "Darwin" ] && command -v dscl >/dev/null 2>&1; then
                _login_shell=$(dscl . -read "/Users/$USER" UserShell 2>/dev/null | awk '{print $2}')
              fi
              if [ -z "$_login_shell" ] && command -v getent >/dev/null 2>&1; then
                _login_shell=$(getent passwd "$USER" 2>/dev/null | cut -d: -f7)
              fi
              if [ -z "$_login_shell" ] && [ -r /etc/passwd ]; then
                _login_shell=$(awk -F: -v u="$USER" '$1==u {print $7}' /etc/passwd)
              fi

              if [ -n "$_login_shell" ] && [ -x "$_login_shell" ] \
                && [ "$(basename "$_login_shell")" != "bash" ]; then
                export CCGATE_DEVSHELL_EXEC=1
                # Prompt marker so users can tell they are inside the nix shell.
                export CCGATE_IN_NIX_SHELL=1
                # Override $SHELL so child processes (e.g. tmux, nvim :term) inherit
                # the user's shell rather than nix's bash.
                export SHELL="$_login_shell"
                exec "$_login_shell"
              fi
              unset _login_shell
            fi

            # Fallback (bash) prompt — only reached when we did not exec into zsh.
            # PROMPT_COMMAND fires before each prompt — wins over /etc/bashrc and nix's prefix.
            PROMPT_COMMAND='PS1="(nix:ccgate) \u\$ "'
          '';
        };
      });
    };
}
