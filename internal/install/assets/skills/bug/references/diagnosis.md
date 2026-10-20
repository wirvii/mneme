# What a diagnosis carries

Write it into the ticket with `backlog_refine`. Another agent, or the person, must be able to act on it without asking you.

1. **Symptom**: what was seen, where, and since when (a commit, a release, a date if known).
2. **Root cause**: the specific line, condition or interaction, not the area. Name files and symbols.
3. **Evidence**: commands or tests anyone can rerun, with the output that shows the cause. Say what you tried that did not explain it.
4. **Confidence**: confirmed (reproduced and explained), very likely (explained, not reproduced), or low (a hypothesis).
5. **Proposal**: the smallest change that removes the cause, the files it touches, the risk of changing them, and a suggested lane.
6. **Open questions**: what you could not check and who could.

A diagnosis, not a fix: if you are tempted to edit code to prove the cause, write a failing test instead and leave it in the ticket as evidence.
