# tui.tape — the second recording: reviewing a run in the terminal UI.
#
# Regenerate with:  vhs docs/tape/tui.tape
# Output:           docs/img/tui.gif
#
# The TUI needs a database with runs in it, and a recording that depended on the author's own
# ~/.batten would be unreproducible by anyone else — and would leak whatever they happened to be
# working on. So it builds its own: `batten demo --dir` leaves a sandbox at a KNOWN path, which
# is the whole reason that flag exists.
#
# BATTEN_DB is exported before the TUI runs. Without it dbPath() falls back to the real database,
# and a recording made for a README would be showing someone's actual work.
#
# REQUIREMENTS: vhs, ttyd, ffmpeg.

Output docs/img/tui.gif

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 800
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms

Hide
Type "export PS1='$ ' && clear" Enter
# Build the sandbox off-camera: the setup is not the story.
Type "rm -rf /tmp/batten-tui-demo && batten demo --keep --dir /tmp/batten-tui-demo > /dev/null" Enter
Sleep 3s
Type "cd /tmp/batten-tui-demo && export BATTEN_DB=.batten/demo.db && clear" Enter
Show

Sleep 800ms
Type "batten tui"
Sleep 600ms
Enter
Sleep 3s

# The run list, then one run's detail: phases, the fan-out, and both verdicts side by side.
Down
Sleep 1s
Enter
Sleep 4s

Escape
Sleep 1500ms
Type "q"
Sleep 1s

Hide
Type "cd - > /dev/null && rm -rf /tmp/batten-tui-demo" Enter
Show
