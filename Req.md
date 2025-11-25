# Sticky Shard Assignment

## Overview

The Sticky Shard Assignment feature allows you to pin specific shards to their assigned workers, preventing automatic rebalancing or stealing of those shards. This is controlled via an optional `Sticky` column in the DynamoDB lease table.

## How It Works

When a worker discovers shards during its event loop:
1. It reads the `Sticky` column value from the DynamoDB lease table
2. Based on the value, it determines whether the shard can be reassigned:
   - **-1, 0, or < 0**: Normal behavior (shard participates in rebalancing)
   - **10**: Sticky shard (pinned to assigned worker, cannot be stolen)
   - **20**: Release signal (worker must gracefully release the shard)
   - **Missing/blank**: Defaults to -1 (normal behavior)

## Sticky Column Values

| Value | Behavior | Description |
|-------|----------|-------------|
| -1 or < 0 | Normal | Shard can be reassigned via normal rebalancing |
| 0 | Normal | Explicitly use normal rebalancing behavior |
| 10 | Sticky | Shard is pinned to its assigned worker, worker can renew lease |
| 20 | Release | Worker must gracefully release shard and clear assignment |
| Missing/blank | Normal | Defaults to -1, normal rebalancing applies |

## Implementation Details

### Read-Only Column

The `Sticky` column is **READ-ONLY** from the worker's perspective:
- Workers only read the value from DynamoDB
- Workers never write or update the sticky value
- The column must be managed externally (e.g., by administrators or automation scripts)

### DynamoDB Schema

Add the `Sticky` column to your DynamoDB lease table:

```
Sticky (Number, Optional):
  - Type: Number
  - Optional: Yes
  - Values: -1, 0, 10, or 20
  - Default: -1 (if missing)
```

**Example using AWS CLI:**

```bash
# Set a shard as sticky (pinned to its current worker)
aws dynamodb update-item \
    --table-name YourLeaseTable \
    --key '{"ShardID": {"S": "shardId-000000000000"}}' \
    --update-expression "SET Sticky = :sticky" \
    --expression-attribute-values '{":sticky": {"N": "10"}}'

# Set a shard for graceful release (worker will release it)
aws dynamodb update-item \
    --table-name YourLeaseTable \
    --key '{"ShardID": {"S": "shardId-000000000000"}}' \
    --update-expression "SET Sticky = :sticky" \
    --expression-attribute-values '{":sticky": {"N": "20"}}'

# Set a shard back to normal (allow rebalancing)
aws dynamodb update-item \
    --table-name YourLeaseTable \
    --key '{"ShardID": {"S": "shardId-000000000000"}}' \
    --update-expression "SET Sticky = :sticky" \
    --expression-attribute-values '{":sticky": {"N": "0"}}'

# Remove sticky column (defaults to normal behavior)
aws dynamodb update-item \
    --table-name YourLeaseTable \
    --key '{"ShardID": {"S": "shardId-000000000000"}}' \
    --update-expression "REMOVE Sticky"
```

### Behavior in Event Loop

During the worker's event loop (shard assignment):

1. **Sticky shard assigned to another worker**: Skip (cannot acquire)
   ```
   Shard X is sticky=10 and assigned to worker-2
   Current worker: worker-1
   Result: Skip this shard
   ```

2. **Sticky shard assigned to current worker**: Allow (lease renewal)
   ```
   Shard X is sticky=10 and assigned to worker-1
   Current worker: worker-1
   Result: Allow lease renewal
   ```

3. **Sticky shard unassigned**: Allow (first assignment)
   ```
   Shard X is sticky=10 with no assignment
   Current worker: worker-1
   Result: Allow initial assignment
   ```

4. **Non-sticky shard**: Normal behavior
   ```
   Shard X is sticky=0 or -1
   Result: Normal rebalancing applies
   ```

### Behavior in Rebalancing

During load balancing (lease stealing):

1. **Sticky shards are filtered out**: Workers will not attempt to steal shards with `sticky=10`
2. **Only non-sticky shards eligible**: Only shards with `sticky < 10` can be stolen
3. **No eligible shards**: If all shards are sticky, rebalancing is skipped

```
Worker distribution:
  worker-1: [shard-1 (sticky=10), shard-2 (sticky=0), shard-3 (sticky=0)]
  worker-2: []

Rebalancing attempt by worker-2:
  - shard-1: Skipped (sticky=10)
  - shard-2: Eligible for stealing
  - shard-3: Eligible for stealing
  Result: worker-2 can steal shard-2 or shard-3
```

## Use Cases

### 1. Dedicated Processing for Critical Shards

Pin critical shards to specific high-performance workers:

```bash
# Pin critical shard to dedicated worker
aws dynamodb update-item \
    --table-name MyAppLeases \
    --key '{"ShardID": {"S": "critical-shard-001"}}' \
    --update-expression "SET Sticky = :sticky, AssignedTo = :worker" \
    --expression-attribute-values '{
        ":sticky": {"N": "10"},
        ":worker": {"S": "high-perf-worker-1"}
    }'
```

### 2. Testing and Debugging

Lock shards during testing to ensure consistent processing:

```bash
# Lock test shard during debugging
aws dynamodb update-item \
    --table-name MyAppLeases \
    --key '{"ShardID": {"S": "test-shard"}}' \
    --update-expression "SET Sticky = :sticky" \
    --expression-attribute-values '{":sticky": {"N": "10"}}'
```

### 3. Gradual Migration

During worker migrations, gradually unpin shards:

```bash
# Phase 1: Pin all shards to old workers
for shard in shard-001 shard-002 shard-003; do
    aws dynamodb update-item \
        --table-name MyAppLeases \
        --key "{\"ShardID\": {\"S\": \"$shard\"}}" \
        --update-expression "SET Sticky = :sticky" \
        --expression-attribute-values '{":sticky": {"N": "10"}}'
done

# Phase 2: Unpin one shard at a time to allow rebalancing
aws dynamodb update-item \
    --table-name MyAppLeases \
    --key '{"ShardID": {"S": "shard-001"}}' \
    --update-expression "SET Sticky = :sticky" \
    --expression-attribute-values '{":sticky": {"N": "0"}}'
```

## Race Condition Protection

The implementation includes protection against race conditions where `syncShard()` might delete shards while `rebalance()` is accessing them:

1. **Existence checks**: Before accessing `w.shardStatus[shard.ID]`, the code checks if the shard exists
2. **Graceful handling**: If a shard is deleted, it's skipped with a debug log message
3. **No panics**: Nil pointer dereferences are prevented

## Code Example: Monitoring Sticky Shards

```go
package main

import (
    "github.com/aws/aws-sdk-go/aws"
    "github.com/aws/aws-sdk-go/aws/session"
    "github.com/aws/aws-sdk-go/service/dynamodb"
)

func listStickyShards(tableName string) error {
    sess := session.Must(session.NewSession())
    svc := dynamodb.New(sess)
    
    result, err := svc.Scan(&dynamodb.ScanInput{
        TableName: aws.String(tableName),
        FilterExpression: aws.String("Sticky = :sticky"),
        ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
            ":sticky": {N: aws.String("10")},
        },
    })
    if err != nil {
        return err
    }
    
    for _, item := range result.Items {
        shardID := aws.StringValue(item["ShardID"].S)
        assignedTo := aws.StringValue(item["AssignedTo"].S)
        println("Sticky shard:", shardID, "assigned to:", assignedTo)
    }
    
    return nil
}
```

## Sticky=20: Graceful Shard Release

### Overview

Setting a shard's sticky value to `20` signals the worker to gracefully release the shard. This is useful for:
- Draining shards before worker shutdown
- Maintenance operations
- Preparing shards for reassignment
- Temporarily pausing shard processing

### How It Works

When a worker detects `sticky=20` during lease renewal:

1. **Detection**: Worker checks sticky value every lease renewal period (default: every few seconds)
2. **Completion**: Worker finishes processing the current batch of records
3. **Checkpoint**: Worker checkpoints its current progress
4. **Release**: Worker clears the `AssignedTo` field in DynamoDB
5. **Exit**: Worker gracefully exits the shard processing loop

### Detection Timing

The worker checks for `sticky=20` during:
- **Lease Renewal**: Checked every `LeaseRefreshPeriodMillis` (typically every 5-10 seconds)
- This ensures relatively quick detection and response

### Behavior Details

**During Event Loop (Shard Assignment)**:
- Workers skip acquiring shards with `sticky=20`
- Even if unassigned, no worker will attempt to acquire these shards
- This prevents immediate reassignment after release

**During Lease Renewal (Shard Processing)**:
- Worker detects `sticky=20` after refreshing lease
- Completes processing current records in flight
- Checkpoints progress to preserve position
- Clears `AssignedTo` field (calls `RemoveLeaseOwner`)
- Exits processing loop gracefully

**During Rebalancing**:
- Shards with `sticky=20` are excluded from stealing candidates
- No worker can steal a `sticky=20` shard

### Workflow Example

**Step 1: Set shard to sticky=20**
```bash
aws dynamodb update-item \
    --table-name YourLeaseTable \
    --key '{"ShardID": {"S": "shardId-000000000000"}}' \
    --update-expression "SET Sticky = :sticky" \
    --expression-attribute-values '{":sticky": {"N": "20"}}'
```

**Step 2: Wait for worker to release**
The assigned worker will:
- Detect the change within ~5-10 seconds (next lease renewal)
- Finish processing current batch
- Checkpoint progress
- Clear the `AssignedTo` field
- Log: `Successfully released shard shardId-000000000000 (sticky=20)`

**Step 3: Verify release**
```bash
aws dynamodb get-item \
    --table-name YourLeaseTable \
    --key '{"ShardID": {"S": "shardId-000000000000"}}'
```

Check that `AssignedTo` is empty or missing.

**Step 4: Change sticky value for reassignment**
```bash
# Set back to normal to allow auto-assignment
aws dynamodb update-item \
    --table-name YourLeaseTable \
    --key '{"ShardID": {"S": "shardId-000000000000"}}' \
    --update-expression "SET Sticky = :sticky" \
    --expression-attribute-values '{":sticky": {"N": "0"}}'

# Or set to sticky=10 for pinned assignment
aws dynamodb update-item \
    --table-name YourLeaseTable \
    --key '{"ShardID": {"S": "shardId-000000000000"}}' \
    --update-expression "SET Sticky = :sticky" \
    --expression-attribute-values '{":sticky": {"N": "10"}}'
```

### Key Points

- **Read-Only**: Workers only read the sticky value, never write it
- **Graceful**: Workers complete current batch before releasing
- **Checkpoint Preserved**: Progress is saved for future workers
- **No Auto-Reassignment**: Shard stays unassigned until sticky value changes
- **External Control**: External process must manage sticky value transitions
- **Quick Detection**: Typically detected within 5-10 seconds

### Use Cases

1. **Controlled Draining**: Gracefully drain specific shards before scaling down
2. **Maintenance Mode**: Temporarily stop processing specific shards
3. **Worker Migration**: Move shard processing from one worker to another
4. **Testing**: Isolate shards for testing or debugging

## Important Notes

### 1. Sticky=10: Lease Renewal Allowed

Workers CAN renew leases on their own sticky=10 shards:
- A worker assigned to a sticky=10 shard can continue processing it
- Lease renewal happens normally for the assigned worker
- Only OTHER workers are prevented from acquiring the shard
- This ensures continuous processing without interruption

### 2. No Automatic Failover for Sticky Shards

If a worker with sticky=10 shards crashes:
- The sticky shards remain assigned to that worker
- Other workers cannot steal them
- The shards will only be processed again when:
  - The original worker restarts, OR
  - The sticky value is changed to allow rebalancing, OR
  - The lease expires and sticky value is changed

### 3. Sticky=20 Requires External Management

Shards with sticky=20:
- Remain unassigned after release
- Will NOT be auto-assigned to any worker
- Require external process to change sticky value before reassignment
- Stay in "paused" state until explicitly changed

### 4. Schema Migration Not Required

The sticky column is optional:
- Existing tables work without modification
- Missing column defaults to normal behavior
- No downtime required for adoption

### 5. Backwards Compatibility

This feature is fully backwards compatible:
- Workers without sticky support ignore the column
- Workers with sticky support handle missing columns gracefully
- Mixed worker versions can coexist

### 6. Monitoring Recommendations

Monitor these metrics when using sticky shards:
- Number of sticky shards per worker
- Lease expiration for sticky shards
- Worker health for workers with sticky assignments

## Troubleshooting

### Sticky Shard Not Processing

**Problem**: Sticky shard assigned to a worker that's not running

**Solution**:
```bash
# Option 1: Restart the assigned worker

# Option 2: Unpin the shard to allow rebalancing
aws dynamodb update-item \
    --table-name YourLeaseTable \
    --key '{"ShardID": {"S": "stuck-shard"}}' \
    --update-expression "SET Sticky = :sticky" \
    --expression-attribute-values '{":sticky": {"N": "0"}}'
```

### Cannot Rebalance Shards

**Problem**: Worker has too many sticky shards, new workers can't acquire any

**Solution**:
```bash
# List all shards and their sticky status
aws dynamodb scan \
    --table-name YourLeaseTable \
    --projection-expression "ShardID,Sticky,AssignedTo"

# Unpin some shards to allow rebalancing
aws dynamodb update-item \
    --table-name YourLeaseTable \
    --key '{"ShardID": {"S": "movable-shard"}}' \
    --update-expression "SET Sticky = :sticky" \
    --expression-attribute-values '{":sticky": {"N": "0"}}'
```

## See Also

- [AWS Kinesis Client Library Documentation](https://docs.aws.amazon.com/streams/latest/dev/shared-throughput-kcl-consumers.html)
- [DynamoDB Conditional Writes](https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/WorkingWithItems.html#WorkingWithItems.ConditionalUpdate)
- [KCL Configuration Options](./clientlibrary/config/config.go)