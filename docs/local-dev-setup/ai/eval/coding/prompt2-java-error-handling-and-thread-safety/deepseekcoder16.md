```shell
ollama run --verbose deepseek-coder-v2:16b "Design a thread-safe InventoryManager class in Java. Use a ConcurrentHashMap to store item IDs (String) and their quantities (Integer). Implement a method updateStock(String id, int delta) that updates the quantity. Ensure the method prevents the stock from going below zero and throws a custom InsufficientStockException if the delta would result in a negative balance. Use a Java Record for the Exception and ensure the update is atomic."
```
To create a thread-safe `InventoryManager` class in Java using a `ConcurrentHashMap`, you can follow these steps:

1. Define a record for the custom exception, `InsufficientStockException`.
2. Implement the `InventoryManager` class with methods to interact with the inventory, ensuring they are thread-safe.
3. Use a `ConcurrentHashMap<String, Integer>` to store item IDs and their quantities.
4. Implement the `updateStock(String id, int delta)` method that atomically updates the stock count, handling exceptions for negative balances.

Here's how you can implement this:

```java
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicInteger;

record InsufficientStockException(String message) {
    public InsufficientStockException {
        if (message == null || message.isEmpty()) {
            throw new IllegalArgumentException("Message must not be empty.");
        }
    }
}

public class InventoryManager {
    private final ConcurrentHashMap<String, AtomicInteger> inventory;

    public InventoryManager() {
        this.inventory = new ConcurrentHashMap<>();
    }

    public void updateStock(String id, int delta) throws InsufficientStockException {
        if (delta == 0) return; // No change requested

        while (true) {
            AtomicInteger currentCount = inventory.getOrDefault(id, new AtomicInteger(0));
            int newCount = currentCount.addAndGet(delta);

            if (newCount < 0) {
                throw new InsufficientStockException("Insufficient stock for item ID: " + id);
            } else if (currentCount.compareAndSet(currentCount.get(), newCount)) {
                return; // Successfully updated the stock count
            }
        }
    }

    public int getStock(String id) {
        AtomicInteger currentCount = inventory.getOrDefault(id, new AtomicInteger(0));
        return currentCount.get();
    }

    public static void main(String[] args) {
        InventoryManager manager = new InventoryManager();
        try {
            manager.updateStock("123", 5); // Adding 5 items
            System.out.println("Current stock of item 123: " + manager.getStock("123"));
            manager.updateStock("123", -8); // Trying to remove more than available (should throw exception)
        } catch (InsufficientStockException e) {
            System.err.println(e.getMessage());
        }
    }
}
```

### Explanation:
- **Record for Exception**: The `InsufficientStockException` is a record that encapsulates an error message. It includes a constructor to ensure the
  message is not null or empty, which helps in handling exceptions more effectively.
- **ConcurrentHashMap and AtomicInteger**: A `ConcurrentHashMap<String, AtomicInteger>` is used to store item IDs as keys and their stock counts as
  values. Each stock count is managed by an `AtomicInteger` to ensure atomic operations when updating the stock.
- **updateStock Method**: This method attempts to update the stock of a given item ID by adding or subtracting the specified delta. It uses a loop
  (`while (true)`) with `compareAndSet` to handle concurrent updates, ensuring that if two threads try to update the same item simultaneously, only one
  will succeed, and the other will retry until it succeeds or another condition is met. If the new stock count would be negative, an
  `InsufficientStockException` is thrown.
- **getStock Method**: This method simply returns the current stock level of a given item ID without modifying the inventory state.

This implementation ensures that all operations on the inventory are thread-safe and handles cases where multiple threads might attempt to update the
same item simultaneously, avoiding race conditions through atomic operations.

---
**Token Throughput**:
- total duration:       22.592678008s
- load duration:        81.217601ms
- prompt eval count:    95 token(s)
- prompt eval duration: 292.077768ms
- prompt eval rate:     325.26 tokens/s
- eval count:           851 token(s)
- eval duration:        21.735457591s
- eval rate:            39.15 tokens/s