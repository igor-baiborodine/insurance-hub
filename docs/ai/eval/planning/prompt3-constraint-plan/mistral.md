```shell
ollama run --verbose mistral “Design a 3–5 step deployment plan for a web service that must have zero downtime and a rollback option after each step. Number the steps and clearly mark the rollback for each.”
```

### Title: Zero-Downtime Deployment Plan for Web Service with Rollback Option

1. **Preparation (Step 1):**
    - Update the current environment configuration to include the new version of the web service, but without activating it.
    - Ensure that the new version is tested and functional in a staging or testing environment.
    - Configure the load balancer or traffic router to direct incoming requests to the old version (currently active) and outgoing requests to the new
      version (yet inactive).
    - Verify that both the old and new versions are running concurrently without any issues.

   Rollback: Revert the configuration changes, discard the new version, and continue serving requests with the existing (old) version.

2. **Health Check (Step 2):**
    - Perform a health check on the new version to ensure it is functioning correctly under production-like conditions. This could involve monitoring
      server logs, response times, error rates, or other relevant metrics.
    - Once the new version passes the health check, gradually increase its traffic share from the load balancer or traffic router, while
      simultaneously decreasing the old version's share.
    - Maintain a minimum threshold of requests being served by the old version for contingency purposes (e.g., 10%).

   Rollback: Revert the traffic split to serve all incoming requests from the old version and decrease the new version's traffic share until it is
   deactivated entirely.

3. **Final Switchover (Step 3):**
    - Continue monitoring both versions' performance during the transition period.
    - Once confident that the new version is stable and performing well, increase its traffic share to reach 100%, while simultaneously decreasing the
      old version's share to zero.
    - Verify that the old version is no longer receiving any incoming requests and has been gracefully shut down.

   Rollback: If issues arise during or after the final switchover, revert to serving all incoming requests from the old version until the problems
   are resolved.

4. **Post-Deployment (Step 4):**
    - Monitor the new version's performance closely in production for a few days to ensure continued stability and identify any lingering issues that
      may not have been apparent during testing or health checks.
    - Perform any necessary tweaks, fixes, or optimizations to improve the new version's performance if needed.

5. **Documentation (Step 5):**
    - Document the deployment process, including any modifications made and lessons learned during the deployment.
    - Update the configuration management system and relevant documentation with the newly deployed version of the web service.

Rollback: If the new version encounters significant issues that cannot be resolved quickly or easily, revert to serving requests from a previously
stable version (if available) or restore the old version until the problems can be addressed.

This deployment plan aims to minimize downtime and provide an option for rolling back to a previous version if necessary. The plan involves gradually
transitioning between versions while continuously monitoring their performance, allowing for quick corrections if issues arise during deployment.

---
**Token Throughput**
- total duration:       15.088823623s
- load duration:        1.283121666s
- prompt eval count:    45 token(s)
- prompt eval duration: 29.909193ms
- prompt eval rate:     1504.55 tokens/s
- eval count:           680 token(s)
- eval duration:        13.649297036s
- eval rate:            49.82 tokens/s