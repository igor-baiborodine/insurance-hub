```shell
ollama run --verbose codestral:22b "Design a thread-safe InventoryManager class in Java. Use a ConcurrentHashMap to store item IDs (String) and their quantities (Integer). Implement a method updateStock(String id, int delta) that updates the quantity. Ensure the method prevents the stock from going below zero and throws a custom InsufficientStockException if the delta would result in a negative balance. Use a Java Record for the Exception and ensure the update is atomic."
```
Below is an implementation of a thread-safe InventoryManager class using ConcurrentHashMap:

```java
import java.util.concurrent.*;

public class InventoryManager {
    private final ConcurrentMap<String, Integer> stock = new ConcurrentHashMap<>();

    public void updateStock(String id, int delta) throws InsufficientStockException {
        while (true) {
            Integer currentQuantity = stock.get(id);

            if (currentQuantity == null && delta < 0) {
                throw new InsufficientStockException("No such item in stock.");
            } else if (currentQuantity != null && currentQuantity + delta < 0) {
                throw new InsufficientStockException("Not enough stock to complete the operation.");
            }

            Integer newQuantity = (currentQuantity == null) ? delta : currentQuantity + delta;
            if (stock.replace(id, currentQuantity, newQuantity)) {
                return;
            }
        }
    }
}
```

And here is a simple implementation of the InsufficientStockException:

```java
public record InsufficientStockException(String message) extends Exception {}
```

This InventoryManager class implements a method updateStock() that updates the stock in a thread-safe manner. It uses a loop with the
ConcurrentHashMap's replace(K, V, V) method to perform an atomic update of the quantity if the current value hasn't changed since it was read. If
another thread has updated the quantity in between, the replace() method will return false and the loop will repeat, re-reading the latest quantity
from the map. This ensures that updates are atomic and prevents two threads from simultaneously updating the same stock level and causing an
inconsistency.

The InsufficientStockException is a custom exception thrown when the update would result in a negative balance or if the item does not exist in the
inventory. It uses Java's Record feature for concise and immutable class declaration.

---
**Token Throughput**:
- total duration:       59.858306515s
- load duration:        1.957376717s
- prompt eval count:    97 token(s)
- prompt eval duration: 676.811115ms
- prompt eval rate:     143.32 tokens/s
- eval count:           453 token(s)
- eval duration:        57.126868699s
- eval rate:            7.93 tokens/s