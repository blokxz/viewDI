# Stage 9a demo: the four tabs on the mock engines — the Home with its cards and
# what needs you, an app opened in Machines, the tree grouped by app, and the
# Activity feed.
# Record from the repository root with: vhs docs/demos/stage-9a.tape

Output docs/demos/stage-9a.gif
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
Sleep 4s

# Home: one sentence, what needs you, one card per app.
Down Sleep 800ms Down Sleep 800ms
Tab Sleep 800ms Right Sleep 800ms
Type "d" Sleep 1500ms Type "d" Sleep 1500ms Type "d" Sleep 1500ms

# The usage charts, full size, and back.
Tab Type "u" Sleep 2s Type "u" Sleep 1s

# An app opens in Machines, filtered.
Tab Enter Sleep 2s
Escape Sleep 500ms

# The tree grouped by app, the inspector in plain words; v flips to the list.
Type "v" Sleep 1s Type "v" Sleep 1s
Down Down Down Sleep 1s
Type "g" Sleep 1s Type "g" Sleep 1s Type "g" Sleep 1s

# Activity: alerts, jobs and updates in one feed, with filters.
Type "4" Sleep 2s
Right Sleep 800ms Right Sleep 800ms Right Sleep 800ms Right Sleep 800ms
Type "1" Sleep 2s
Type "q"
