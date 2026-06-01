```shell
ollama run --verbose qwen2.5-coder:14b "Design a thread-safe InventoryManager class in Java. Use a ConcurrentHashMap to store item IDs (String) and their quantities (Integer). Implement a method updateStock(String id, int delta) that updates the quantity. Ensure the method prevents the stock from going below zero and throws a custom InsufficientStockException if the delta would result in a negative balance. Use a Java Record for the Exception and ensure the update is atomic."
```

```java
import java.util.concurrent.ConcurrentHashMap;

public class InventoryManager {
    private final ConcurrentHashMap<String, Integer> inventory;

    public InventoryManager() {
        this.inventory = new ConcurrentHashMap<>();
    }

    // Method to update stock safely with atomic operations
    public void updateStock(String id, int delta) throws InsufficientStockException {
        // Use compute method to ensure the operation is atomic
        inventory.compute(id, (key, quantity) -> {
            if (quantity == null) {
                quantity = 0; // Default to zero if item doesn't exist in inventory
            }
            if (delta < 0 && quantity + delta < 0) {
                throw new InsufficientStockException("Cannot update stock below zero for item: " + id);
            }
            return quantity + delta;
        });
    }

    // Java Record for custom exception
    public record InsufficientStockException(String message) extends RuntimeException {
        public InsufficientStockException(String message) {
            super(message);
        }
    }
}
```
This solution defines an `InventoryManager` class that uses a `ConcurrentHashMap` to store item IDs and their quantities. The `updateStock` method
safely updates the stock levels by ensuring that the operation is atomic using the `compute` method of `ConcurrentHashMap`. It checks if updating the
stock would result in a negative value and throws an `InsufficientStockException` if it does, preventing the stock from going below zero.

---
**Token Throughput**
- total duration:       31.717792002s
- load duration:        8.411109589s
- prompt eval count:    113 token(s)
- prompt eval duration: 370.926944ms
- prompt eval rate:     304.64 tokens/s
- eval count:           299 token(s)
- eval duration:        22.678406059s
- eval rate:            13.18 tokens/s