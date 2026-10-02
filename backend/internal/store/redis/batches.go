package redis

import (
	"context"
	"fmt"
	"strconv"
	"uuid"

	"ashen-courier/internal/domain"
	goredis "github.com/redis/go-redis/v9"
)

// A batch stays in Redis until PostgreSQL commits it. No leases or worker-local IDs.
var freezeBatchScript = goredis.NewScript(`
local id = redis.call('HGET', KEYS[2], 'id')
if id then return {id, redis.call('HGET', KEYS[2], 'delta')} end
local n = redis.call('GET', KEYS[1]) or '0'
if tonumber(n) <= 0 then
 redis.call('SREM', KEYS[3], ARGV[2])
 return {'', '0'}
end
redis.call('HSET', KEYS[2], 'id', ARGV[1], 'delta', n)
redis.call('SET', KEYS[4], ARGV[1])
redis.call('DEL', KEYS[1])
return {ARGV[1], n}
`)

var confirmBatchScript = goredis.NewScript(`
if redis.call('HGET', KEYS[2], 'id') ~= ARGV[1] then return 0 end
redis.call('DEL', KEYS[2])
if tonumber(redis.call('GET', KEYS[1]) or '0') <= 0 then
 redis.call('SREM', KEYS[3], ARGV[2])
end
return 1
`)

func batchKey(code string) string { return "clicks:batch:v2:" + code }
func epochKey(code string) string { return "clicks:epoch:v2:" + code }

func (c *Client) FreezeBatch(ctx context.Context, code string) (domain.CountBatch, error) {
	op, cancel := c.opCtx(ctx)
	defer cancel()
	id := uuid.NewV7()
	values, err := freezeBatchScript.Run(op, c.rdb,
		[]string{ClickCounterKey(code), batchKey(code), dirtySetKey, epochKey(code)}, id.String(), code).StringSlice()
	if err != nil {
		return domain.CountBatch{}, fmt.Errorf("freeze count batch: %w", err)
	}
	delta, err := strconv.ParseInt(values[1], 10, 64)
	return domain.CountBatch{ID: values[0], Delta: delta}, err
}

func (c *Client) ConfirmBatch(ctx context.Context, code string, batch domain.CountBatch) error {
	if batch.ID == "" {
		return nil
	} // Freeze already cleaned an empty dirty entry atomically.
	op, cancel := c.opCtx(ctx)
	defer cancel()
	return confirmBatchScript.Run(op, c.rdb,
		[]string{ClickCounterKey(code), batchKey(code), dirtySetKey}, batch.ID, code).Err()
}

var snapshotsScript = goredis.NewScript(`
local out = {}
for i = 1, #KEYS, 3 do
 table.insert(out, redis.call('GET', KEYS[i]) or '0')
 table.insert(out, redis.call('HGET', KEYS[i+1], 'id') or '')
 table.insert(out, redis.call('HGET', KEYS[i+1], 'delta') or '0')
 table.insert(out, redis.call('GET', KEYS[i+2]) or '')
end
return out
`)

func (c *Client) CountSnapshots(ctx context.Context, codes []string) (map[string]domain.CountSnapshot, error) {
	out := make(map[string]domain.CountSnapshot, len(codes))
	if len(codes) == 0 {
		return out, nil
	}
	keys := make([]string, 0, len(codes)*3)
	for _, code := range codes {
		keys = append(keys, ClickCounterKey(code), batchKey(code), epochKey(code))
	}
	op, cancel := c.opCtx(ctx)
	defer cancel()
	vals, err := snapshotsScript.Run(op, c.rdb, keys).StringSlice()
	if err != nil {
		return nil, err
	}
	for i, code := range codes {
		active, err := strconv.ParseInt(vals[i*4], 10, 64)
		if err != nil {
			return nil, err
		}
		delta, err := strconv.ParseInt(vals[i*4+2], 10, 64)
		if err != nil {
			return nil, err
		}
		out[code] = domain.CountSnapshot{Active: active, Batch: domain.CountBatch{ID: vals[i*4+1], Delta: delta}, Epoch: vals[i*4+3]}
	}
	return out, nil
}
