```shell
ollama run --verbose phi4-mini “Design a 3–5 step deployment plan for a web service that must have zero downtime and a rollback option after each step. Number the steps and clearly mark the rollback for each.”
```

**Step-by-Step Deployment Plan with Zero Downtime:**

### Step 1
Deploy a new version of Web Service:
- Deploy to a staging environment.

*Rollback*: If there are any issues, revert back by redeploying the previous stable release in a similar manner.

---

### Step 2 (Blue-Green deployment)
Switch traffic from current live production service to newly deployed services on Blue stage:

1. Set up two identical environments: Current Live Environment (Green) and New Deployment Stage (Blue).

*Rollback*: If there are any issues, switch back by reverting the DNS settings so that all user requests will be redirected back again into Green.

---

### Step 3
Run comprehensive tests in staging environment to ensure it works correctly:

1. Run load testing.
2. Execute smoke test on both Blue and Green environments as well (simultaneously).

*Rollback*: If there are any issues detected during this stage, rollback by switching DNS settings back again so that all user requests will be
redirected into Live Environment.

---

### Step 4
Switch traffic from staging environment to production:

1. Route live users' request flows through the new Blue deployment instead of Green.
2. Monitor application logs and metrics closely for any anomalies or performance issues during this transition period (monitoring).

*Rollback*: If there are any unexpected failures, revert DNS settings back again so that all user requests will be redirected into Live Environment.

---

### Step 5
Monitor newly deployed services:

1. Collect feedback from users.
2. Monitor application logs and metrics continuously to check for performance issues or errors detected in real-time during live operation
   (monitoring).

*Rollback*: If there are any major failures, revert DNS settings back again so that all user requests will be redirected into Live Environment.

---

The rollback strategy used here is a Blue-Green Deployment approach. This method ensures zero downtime and allows seamless switching between
environments without affecting the end users’ experience during deployment or unexpected rollbacks after each step (if required). The key to success
of this plan relies on comprehensive testing in staging before transitioning traffic flow, as well as continuous monitoring post-deployment for a
smooth user-experience transition.

---
**Token Throughput**
- total duration:       7.302517006s
- load duration:        1.724908144s
- prompt eval count:    41 token(s)
- prompt eval duration: 17.31725ms
- prompt eval rate:     2367.58 tokens/s
- eval count:           437 token(s)
- eval duration:        5.193756072s
- eval rate:            84.14 tokens/s