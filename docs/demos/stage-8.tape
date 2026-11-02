# Stage 8 demo: the container library on the mock engines — browse, filter,
# run PostgreSQL through the prefilled form, then the Docker Hub search and the
# unused-images list.
# Record from the repository root with: vhs docs/demos/stage-8.tape

Output docs/demos/stage-8.gif
Set Shell bash
Set FontSize 14
Set Width 1400
Set Height 820
Set Padding 10
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms

Hide
Type "make build >/dev/null && export XDG_CONFIG_HOME=$(mktemp -d) XDG_STATE_HOME=$(mktemp -d) && clear" Enter
Sleep 15s
Show

Type "./bin/viewdi --mock --theme catppuccin-mocha" Sleep 300ms Enter
Sleep 3s

# Browse the library and look at an entry.
Type "7" Sleep 1s
Down Down Down Sleep 1s
Tab Down Down Sleep 1s
Tab Tab Sleep 500ms

# Filter, then run PostgreSQL: the form arrives with a free name, a free port
# and a generated password.
Type "/postgres" Sleep 300ms Enter Sleep 1s
Enter Sleep 3s
Enter Sleep 4s
Escape Sleep 1s

# An Incus entry asks for its download size on its own.
Escape Sleep 500ms
Type "e" Sleep 300ms Type "e" Sleep 1s
Type "/debian-12-docker" Sleep 300ms Enter Sleep 500ms
Enter Sleep 3s
Escape Sleep 1s

# Search Docker Hub, then list the unused images.
Type "S" Sleep 500ms
Type "grafana" Sleep 300ms Enter Sleep 3s
Escape Sleep 500ms
Type "x" Sleep 3s
Escape Sleep 1s
