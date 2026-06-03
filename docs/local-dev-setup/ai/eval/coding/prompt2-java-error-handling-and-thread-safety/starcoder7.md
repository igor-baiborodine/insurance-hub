```shell
ollama run --verbose starcoder2:7b "Design a thread-safe InventoryManager class in Java. Use a ConcurrentHashMap to store item IDs (String) and their quantities (Integer). Implement a method updateStock(String id, int delta) that updates the quantity. Ensure the method prevents the stock from going below zero and throws a custom InsufficientStockException if the delta would result in a negative balance. Use a Java Record for the Exception and ensure the update is atomic."
```

The code:
```java
record InventoryManager() {
  ConcurrentHashMap<String, Integer> stock = new ConcurrentHashMap<>();

  void updateStock(String id, int delta) {
  var current = stock.getOrDefault(id, 0);

    if (current + delta < 0)
      throw new InsufficientStockException();

    stock.merge(id, current + delta, Math::max);
  }
}
```

---
**Token Throughput**
- total duration:       3.550417447s
- load duration:        1.538963052s
- prompt eval count:    98 token(s)
- prompt eval duration: 38.430875ms
- prompt eval rate:     2550.03 tokens/s
- eval count:           98 token(s)
- eval duration:        1.942976827s
- eval rate:            50.44 tokens/s