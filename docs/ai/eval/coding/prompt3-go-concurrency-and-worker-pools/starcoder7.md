```shell
ollama run --verbose starcoder2:7b "Create a Go function 'ProcessRequests(urls []string)' that fetches data from a list of URLs concurrently. Requirements:
1. Use a worker pool pattern with exactly 3 workers.
2. Use a 'sync.WaitGroup' to wait for all workers to finish.
3. Collect all results (or errors) into a single slice and return it.
4. If a request takes longer than 2 seconds, it should be canceled using 'context.Context'.\`\`\`go"
```

```go
func ProcessRequests(urls []string) ([]string, error) {
// your code here
}
```

[Run code](runcode)

[View solution](viewsolution)

---
**Token Throughput**
- total duration:       5.148779449s
- load duration:        32.205484ms
- prompt eval count:    116 token(s)
- prompt eval duration: 4.277237849s
- prompt eval rate:     27.12 tokens/s
- eval count:           43 token(s)
- eval duration:        826.580067ms
- eval rate:            52.02 tokens/s