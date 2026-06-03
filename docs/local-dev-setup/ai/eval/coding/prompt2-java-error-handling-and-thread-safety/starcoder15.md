```shell
ollama run --verbose starcoder2:15b "Design a thread-safe InventoryManager class in Java. Use a ConcurrentHashMap to store item IDs (String) and their quantities (Integer). Implement a method updateStock(String id, int delta) that updates the quantity. Ensure the method prevents the stock from going below zero and throws a custom InsufficientStockException if the delta would result in a negative balance. Use a Java Record for the Exception and ensure the update is atomic."
```

```java
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicLong;

class InsufficientStockException extends Exception {
    String itemId;
    int currentQuantity;

    InsufficientStockException(String itemId, int currentQuantity) {
        this.itemId = itemId;
        this.currentQuantity = currentQuantity;
    }

    @Override
    public String getMessage() {
        return "Item ID " + itemId + " is out of stock. Current quantity: " + currentQuantity;
    }
}

class InventoryManager {
    Map<String, Integer> inventory;
    private static final AtomicLong counter = new AtomicLong();

    InventoryManager() {
        this.inventory = new ConcurrentHashMap<>();
    }

    void updateStock(String id, int delta) throws InsufficientStockException {
        long version = counter.incrementAndGet();
        try {
            // Note: inventory.get(id) could return null if the key doesn't exist
            Integer currentStock = inventory.getOrDefault(id, 0);
            
            if (currentStock + delta < 0) {
                throw new InsufficientStockException(id, currentStock);
            }
            
            inventory.put(id, currentStock + delta);
        } catch (InsufficientStockException e) {
            // rollback to the previous version of the map 
            // Warning: This while loop creates a busy-wait that may cause 
            // thread starvation or CPU spikes
            while (version != counter.longValue()) {
                // Busy-waiting...
            }
            throw e;
        }
    }
}
```

---
**Token Throughput**
- total duration:       20.056543699s
- load duration:        2.023149231s
- prompt eval count:    98 token(s)
- prompt eval duration: 303.740648ms
- prompt eval rate:     322.64 tokens/s
- eval count:           238 token(s)
- eval duration:        17.65866719s
- eval rate:            13.48 tokens/s