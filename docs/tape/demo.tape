# demo.tape — the README's GIF.
#
# Regenerate with:  vhs docs/tape/demo.tape
# Output:           docs/img/demo.gif
#
# WHY THIS IS A .tape AND NOT A GIF SOMEBODY RECORDED ONCE
#
# A screen recording is a claim about software that stops being checkable the moment the
# software changes. This file is code: re-run it and the GIF is regenerated from whatever
# batten actually does today. If the output changes, the GIF changes. It cannot go stale
# while quietly continuing to look convincing.
#
# WHAT IT SHOWS, and why this arc and not another
#
# `batten demo` builds a throwaway repo and walks the whole flow, so the GIF needs no setup
# and touches nothing. The dramatic beat is in the middle: the commit is DENIED, then the
# declared check RUNS and fails for a real reason, then the bug is fixed and the same check
# passes, and only then does the commit land. That sequence is the only way to communicate in
# a README that this is not a linter — it actually stops you.
#
# REQUIREMENTS: vhs, plus its own dependencies ttyd and ffmpeg.
#   https://github.com/charmbracelet/vhs

Output docs/img/demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 800
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms
Set PlaybackSpeed 1.0

# A clean prompt: the GIF is about batten's output, not about whose machine this is.
Hide
Type "export PS1='$ ' && clear" Enter
Show

Sleep 800ms
Type "batten demo"
Sleep 600ms
Enter

# The walkthrough prints as it goes. The sleeps are placed at the beats that matter, so a
# reader scrubbing the GIF lands on a denial rather than on a wall of setup.
Sleep 3s

# init, the ungoverned commit, the run opening.
Sleep 3s

# The two denials — no verdict, then reviewer-only. This is the point of the whole recording.
Sleep 4s

# The write-set collision.
Sleep 3s

# The check running, failing, being fixed, passing.
Sleep 4s

# The commit landing, and the report.
Sleep 5s

Type "# the sandbox is gone. Nothing of yours was touched."
Sleep 2s
Enter
Sleep 2s
