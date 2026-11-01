# Stage 5a demo: the Tree view on the mock engines — moving through the
# network grouping with the inspector following, a machine's leaves, J
# between its networks, the other groupings, the filter, and an action.
# Record from the repository root with: vhs docs/demos/stage-5a.tape

Output docs/demos/stage-5a.gif
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
Type "4" Sleep 2s

# Down the network grouping: the inspector follows the cursor.
Down 9 Sleep 1500ms
Right Sleep 1500ms
Down@400ms 3 Sleep 1500ms
Up@400ms 3 Sleep 500ms
Type "J" Sleep 2s
Type "J" Sleep 1500ms
Left Sleep 1s

# Other groupings keep the same machine under the cursor.
Type "g" Sleep 2s
Type "g" Sleep 2s
Type "g" Sleep 2s
Type "g" Sleep 1500ms

# Filter by IP, then by name, and jump between matches.
Type "/" Sleep 500ms
Type "10.10" Sleep 1500ms
Enter Sleep 500ms
Ctrl+N Sleep 1s
Ctrl+N Sleep 1s
Escape Sleep 1500ms

# Fold everything, open again, and stop a machine from the tree.
Type "-" Sleep 1500ms
Type "*" Sleep 1500ms
Type "/" Sleep 300ms
Type "redis" Sleep 800ms
Enter Sleep 500ms
Escape Sleep 800ms
Type "x" Sleep 3s
Type "?" Sleep 2s
Escape Sleep 500ms
Type "q"
Sleep 500ms
