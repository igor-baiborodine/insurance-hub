```shell
ollama run --verbose deepseek-r1:8b “You have tasks A, B, C, D, E and can only do 3 this week. Describe a simple prioritization strategy and pick the 3 tasks with a one-sentence justification each.”
```
Thinking...
Okay, a user has five tasks (A, B, C, D, E) but can only do three this week. They're asking for a simple prioritization strategy and which three tasks       
to pick with justifications.

Hmm, this seems like a common time management scenario. The user might be feeling overwhelmed or just wants a quick decision-making framework. They        
didn't provide task details, so I'll assume they want a generalizable method.

First, I should explain why prioritization matters - otherwise they might just randomly pick three. The strategy should be simple but effective.           
DEADLINE, IMPACT, BLOCKER seems like a good trio of criteria because it covers urgency, value, and dependencies.

For picking tasks, I'll need to make some reasonable assumptions since they didn't specify. Let's say Task A is most critical (high impact), Task B        
is time-sensitive (deadline), and Task C is foundational (blocker). The other two would be deprioritized - maybe Task D is low-value or Task E             
blocked.

The justifications should be concise but cover different angles: one for strategic importance, one for time pressure, one for dependencies. This           
shows a multi-faceted approach.

User probably wants this to be actionable immediately, so I'll keep the language direct. They didn't ask for alternatives, but I'll mention other          
methods exist in case they need more options later.

The tone should be helpful but not prescriptive - they're the expert on their tasks, just asking for guidance. I'll avoid jargon since they asked for      
"simple."                                                                                                                                                  
...done thinking.

Okay, here's a simple prioritization strategy and selection:

**Prioritization Strategy:**

Use three criteria:
1.  **DEADLINE:** Does the task have an upcoming deadline this week? (Most urgent)
2.  **IMPACT:** Will completing the task have the biggest positive effect on your goals or others? (Most valuable)
3.  **BLOCKER:** Does doing this task enable you to do other important tasks? (Foundation/enabler)

**Selected Tasks:**

1.  **Task A:** *Has the most critical deadline this week.* (Prioritized based on DEADLINE)
2.  **Task B:** *Provides the highest potential impact or value.* (Prioritized based on IMPACT)
3.  **Task C:** *Is a blocker for completing other potentially important tasks.* (Prioritized based on BLOCKER)

*(Assumptions: Task D has a low priority or no deadline/impact this week. Task E is blocked by another task or has the lowest priority.)*

---
**Token Throughput**
- total duration:       15.539001503s
- load duration:        3.066647049s
- prompt eval count:    43 token(s)
- prompt eval duration: 37.990544ms
- prompt eval rate:     1131.86 tokens/s
- eval count:           532 token(s)
- eval duration:        12.256466278s
- eval rate:            43.41 tokens/s