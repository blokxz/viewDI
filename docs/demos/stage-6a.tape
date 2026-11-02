# Stage 6a demo: update discovery on the mock engines — the first check
# 3 s after startup (fake sources, no network), the toast and the header
# badge, the markers in the Table and the Tree inspector, the admin alerts,
# then the Updates view: release notes, snooze and ignore, and U.
# Record from the repository root with: vhs docs/demos/stage-6a.tape

Output docs/demos/stage-6a.gif
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
# The first check runs 3 s after startup in --mock: toast, badge, alerts.
Sleep 6s

# Markers follow the instances in the Table and the Tree.
Type "3" Sleep 3s
Type "4" Sleep 1s
Down@500ms 3 Sleep 3s

# The Updates view: engines, then images; the footer follows the cursor.
Type "6" Sleep 3s
Down@700ms 3 Sleep 2s
Up@700ms 3 Sleep 1s

# Snooze Docker for 7 days, ignore an image version: the badge drops.
Type "z" Sleep 2500ms
Down@500ms 2 Sleep 500ms
Type "I" Sleep 2500ms
# Both toggle back.
Type "I" Sleep 1500ms
Up@500ms 2 Sleep 500ms
Type "z" Sleep 2000ms

# Check now: nothing new, so no toast.
Type "U" Sleep 4s
Type "q"
