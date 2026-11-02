# Stage 6b demo: acting on image updates on the mock engines — pull a Docker
# image (the row turns "pulled · recreate"), recreate its container after the
# preview of what is kept and the confirm, watch the job in Jobs, see the
# image re-checked and gone, then refresh an Incus image.
# Record from the repository root with: vhs docs/demos/stage-6b.tape

Output docs/demos/stage-6b.gif
Set Shell bash
Set FontSize 14
Set Width 1400
Set Height 820
Set Padding 10
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms

Hide
Type "make build >/dev/null && export XDG_CONFIG_HOME=$(mktemp -d) XDG_STATE_HOME=$(mktemp -d) XDG_DATA_HOME=$(mktemp -d) XDG_CACHE_HOME=$(mktemp -d) && clear" Enter
Sleep 15s
Show

Type "./bin/viewdi --mock --theme catppuccin-mocha" Sleep 300ms Enter
# The first check runs 3 s after startup in --mock.
Sleep 6s

# The Updates view: down to nginx:1.27 and pull it.
Type "6" Sleep 2s
Down@700ms 3 Sleep 1500ms
Type "p" Sleep 5s

# The row now reads "pulled · recreate": preview what is kept, then confirm.
Type "c" Sleep 4s
Type "y" Sleep 2500ms
Type "y" Sleep 1500ms

# Jobs shows the phases: pulling, stopping, renaming, creating, starting.
Type "5" Sleep 5s

# Back in Updates the image check ran again: nginx:1.27 is gone.
Type "6" Sleep 3s

# Refresh the Incus image: its instances keep running until rebuilt.
Down@700ms 1 Sleep 1s
Type "f" Sleep 4s
Type "5" Sleep 3s
Type "q"
