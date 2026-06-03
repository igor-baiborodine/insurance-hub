```shell
ollama run --verbose qwen2.5-coder:7b "Design a thread-safe InventoryManager class in Java. Use a ConcurrentHashMap to store item IDs (String) and their quantities (Integer). Implement a method updateStock(String id, int delta) that updates the quantity. Ensure the method prevents the stock from going below zero and throws a custom InsufficientStockException if the delta would result in a negative balance. Use a Java Record for the Exception and ensure the update is atomic."
```

```java
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicInteger;

// Custom exception record to indicate insufficient stock
public record InsufficientStockException(String itemId, int required) extends RuntimeException {
    public InsufficientStockException(String itemId, int required) {
        super("Insufficient stock for item: " + itemId + ". Required: " + required);
    }
}

// Thread-safe InventoryManager class using ConcurrentHashMap and AtomicInteger
public class InventoryManager {
    private final ConcurrentHashMap<String, AtomicInteger> inventory;

    // Constructor to initialize the inventory
    public InventoryManager() {
        this.inventory = new ConcurrentHashMap<>();
    }

    // Method to update stock by a given delta, ensuring it doesn't go below zero
    public void updateStock(String id, int delta) throws InsufficientStockException {
        while (true) {
            AtomicInteger currentStock = inventory.get(id);
            if (currentStock == null) {
                // If the item is not in the inventory yet, initialize with zero
                currentStock = new AtomicInteger(0);
                if (inventory.putIfAbsent(id, currentStock) != null) {
                    continue; // Retry if another thread added the item meanwhile
                }
            }

            int updatedStock = currentStock.get() + delta;
            if (updatedStock < 0) {
                throw new InsufficientStockException(id, Math.abs(delta));
            } else if (currentStock.compareAndSet(currentStock.get(), updatedStock)) {
                break; // Update was successful, exit the loop
            }
        }
    }

    // Method to get the current stock for an item
    public int getStock(String id) {
        AtomicInteger stock = inventory.get(id);
        return stock != null ? stock.get() : 0;
    }
}
```

This code snippet defines a thread-safe `InventoryManager` class that uses a `ConcurrentHashMap` to store item IDs and their quantities, each managed
by an `AtomicInteger`. The `updateStock` method updates the quantity for a given item ID atomically, ensuring it doesn't go below zero. If the update
would result in a negative balance, it throws a custom `InsufficientStockException`. The use of `compareAndSet` ensures that the stock update is
thread-safe and atomic.

---
**Token Throughput**
- total duration:       14.97360993s
- load duration:        5.135681857s
- prompt eval count:    113 token(s)
- prompt eval duration: 42.961247ms
- prompt eval rate:     2630.28 tokens/s
- eval count:           464 token(s)
- eval duration:        9.428289468s
- eval rate:            49.21 tokens/s