# Stage 5b demo: labs on the mock engines — the lab grouping with its
# animated lanes, the cursor highlighting a machine's links, a stopped end
# turning its lanes grey, then the L menu: a new lab, a link across it,
# and deleting the lab through the confirm modal.
# Record from the repository root with: vhs docs/demos/stage-5b.tape

Output docs/demos/stage-5b.gif
Set Shell bash
Set FontSize 14
Set Width 1400
Set Height 820
Set Padding 10
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms

Hide
Type "make build >/dev/null && export XDG_CONFIG_HOME=$(mktemp -d) XDG_STATE_HOME=$(mktemp -d) XDG_DATA_HOME=$(mktemp -d) && clear" Enter
Sleep 15s
Show

Type "./bin/viewdi --mock --theme catppuccin-mocha" Sleep 300ms Enter
Sleep 3s
Type "4" Sleep 1500ms

# The lab grouping: the demo lab spans both engines; dots run on live links.
Type "g" Sleep 400ms
Type "g" Sleep 400ms
Type "g" Sleep 400ms
Type "g" Sleep 3s

# The cursor highlights a machine's links; the inspector lists them.
Down@700ms 5 Sleep 2s
Up@700ms 3 Sleep 2s

# Stop an end: its lanes turn grey and still. Start it again.
Type "x" Sleep 4s
Type "s" Sleep 4s

# A new lab from a machine in no lab, then a link across engines' machines.
Down@200ms 6 Sleep 500ms
Right Sleep 800ms
Down@300ms 4 Sleep 1s
Type "L" Sleep 1500ms
Enter Sleep 800ms
Type "monitoring" Sleep 800ms
Enter Sleep 2s
Down Sleep 800ms
Type "L" Sleep 1500ms
Down@400ms 2 Sleep 600ms
Enter Sleep 1500ms
Down@200ms 9 Sleep 800ms
Enter Sleep 3s

# Delete the lab: the machines are not touched.
Up Sleep 800ms
Type "L" Sleep 1200ms
Up Sleep 600ms
Enter Sleep 2s
Type "y" Sleep 2500ms
Type "q"
Sleep 500ms
