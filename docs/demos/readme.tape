# README demo: a tour of the current viewDI on the mock engines — the Home with
# its cards and what needs you, the Machines tree and its inspector, the
# container library, the Activity feed and the command palette.
# Record from the repository root, with the binary already built (make build):
#   vhs docs/demos/readme.tape

Output docs/demos/readme.gif
Set Shell bash
Set FontSize 14
Set Width 1400
Set Height 820
Set Padding 10
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms

Hide
Type "export XDG_CONFIG_HOME=$(mktemp -d) XDG_STATE_HOME=$(mktemp -d) && clear" Enter
Sleep 1s
Show

Type "./bin/viewdi --mock --theme catppuccin-mocha" Sleep 300ms Enter
Sleep 4s

# Home: one sentence, what needs you, one card per app.
Down Sleep 800ms Down Sleep 800ms
Tab Sleep 800ms Right Sleep 800ms
Type "d" Sleep 1500ms Type "d" Sleep 1500ms

# Machines: the tree grouped by app, with the inspector in plain words.
Type "2" Sleep 2s
Down Down Down Sleep 1s
Type "g" Sleep 1s Type "g" Sleep 1s

# Library: pick an image or a system and enter fills the create form.
Type "3" Sleep 2s
Down Down Down Sleep 1s

# Activity: alerts, jobs and updates in one feed.
Type "4" Sleep 2s
Right Sleep 800ms Right Sleep 800ms

# The palette finds anything and teaches its key.
Ctrl+K Sleep 800ms
Type "restart web" Sleep 1500ms
Escape Sleep 500ms
Type "1" Sleep 1500ms
Type "q"
