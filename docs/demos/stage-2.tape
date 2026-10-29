# Stage 2 demo: Admin dashboard (daemon cards, alerts, events), Resources
# dashboard (gauges, sparklines, top consumers, refresh rate) and live table
# metrics, with an Incus outage from --mock=chaos.
# Record from the repository root with: vhs docs/demos/stage-2.tape

Output docs/demos/stage-2.gif
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

Type "./bin/viewdi --mock=chaos --theme catppuccin-mocha" Sleep 300ms Enter
Sleep 4s

# Resources: gauges, sparklines, top consumers; sort and refresh rate.
Type "2" Sleep 5s
Type "o" Sleep 1500ms
Type "o" Sleep 1500ms
Type "+" Sleep 3s
Type "-" Sleep 1s

# Admin: the chaos scenario stops Incus at 5 s; alerts and events follow.
Type "1" Sleep 6s

# Table with live CPU and memory.
Type "3" Sleep 4s

# Back to the dashboards for the recovery.
Type "2" Sleep 6s
Type "1" Sleep 4s
Type "q"
Sleep 1s
