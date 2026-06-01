```shell
ollama run --verbose llama3.1:8b “You have tasks A, B, C, D, E and can only do 3 this week. Describe a simple prioritization strategy and pick the 3 tasks with a one-sentence justification each.”
```
Here's a simple prioritization strategy:

**Strategy:** Prioritize tasks based on their Urgency and Importance (UI) score, which combines two factors: **Urgency** (how quickly does the task
need to be completed?) and **Importance** (what are the consequences of not completing the task on time?).

Here are the 3 chosen tasks with a one-sentence justification each:

1. **Task A: "Fix critical bug in current project"**: High urgency, as it's blocking progress on an ongoing project, and high importance, as a
   delayed fix could lead to lost revenue or customer dissatisfaction.
2. **Task C: "Meet quarterly sales targets"**: Medium-high importance, as meeting targets is crucial for business growth and revenue, but medium-low
   urgency, as there are still several weeks left in the quarter to make up for any shortfall.
3. **Task B: "Update company website with new content"**: Medium-low importance, but high-medium urgency, as it's been delayed for too long and needs
   attention to keep the site fresh and engaging for customers.

This prioritization strategy aims to balance short-term needs (fixing a critical bug) with longer-term goals (meeting sales targets), while also
addressing important tasks that have been neglected for too long.

---
**Token Throughput**
- total duration:       7.532702811s
- load duration:        1.832404326s
- prompt eval count:    51 token(s)
- prompt eval duration: 32.758411ms
- prompt eval rate:     1556.85 tokens/s
- eval count:           261 token(s)
- eval duration:        5.516708144s
- eval rate:            47.31 tokens/s