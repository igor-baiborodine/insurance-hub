```shell
ollama run --verbose llama3.1:8b “List 7 concrete steps to plan and ship a small backend feature, from initial requirements to deployment. Group them into 3 phases and name each phase.”
```

Here are the 7 concrete steps to plan and ship a small backend feature, grouped into 3 phases:

**Phase 1: Planning (Steps 1-2)**

1. **Define Requirements**: Clearly articulate the requirement for the new feature based on customer feedback, business needs, or technical debt.
   Identify the key stakeholders involved in the project and document their expectations.
2. **Break Down Requirements**: Break down the requirements into smaller, manageable tasks. This will help to identify potential roadblocks and
   estimate the time required to complete each task.

**Phase 2: Development (Steps 3-5)**

1. **Write Code**: Start writing code based on the broken-down tasks identified in Phase 1. Follow standard coding practices and ensure that the new
   feature is properly tested.
2. **Implement Unit Tests**: Write unit tests for the new feature to ensure it works correctly. Use a testing framework such as Jest or Pytest to
   write and run tests.
3. **Test and Integrate**: Test the new feature in isolation, then integrate it with other parts of the system to ensure there are no regressions.

**Phase 3: Deployment (Steps 6-7)**

1. **Prepare for Deployment**: Ensure that all necessary dependencies are updated, and the code is properly formatted according to your team's
   standards. Create a deployment script or pipeline using tools like Jenkins, CircleCI, or GitHub Actions.
2. **Deploy to Production**: Run the deployment script or pipeline to deploy the new feature to production. Monitor the system for any issues or
   regressions.

By following these 7 concrete steps and grouping them into 3 phases, you can ensure that your small backend feature is developed and deployed
efficiently and with minimal risk.

---
**Token Throughput**
- total duration:       9.497412597s
- load duration:        1.728406393s
- prompt eval count:    42 token(s)
- prompt eval duration: 31.713249ms
- prompt eval rate:     1324.37 tokens/s
- eval count:           352 token(s)
- eval duration:        7.513699061s
- eval rate:            46.85 tokens/s