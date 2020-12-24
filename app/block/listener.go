package block

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/go-redis/redis/v8"
	cfg "github.com/itzmeanjan/ette/app/config"
	d "github.com/itzmeanjan/ette/app/data"
	"gorm.io/gorm"
)

// SubscribeToNewBlocks - Listen for event when new block header is
// available, then fetch block content ( including all transactions )
// in different worker
func SubscribeToNewBlocks(connection *d.BlockChainNodeConnection, _db *gorm.DB, _lock *sync.Mutex, _synced *d.SyncState, redisClient *redis.Client, redisKey string) {
	headerChan := make(chan *types.Header)

	subs, err := connection.Websocket.SubscribeNewHead(context.Background(), headerChan)
	if err != nil {
		log.Fatalf("[!] Failed to subscribe to block headers : %s\n", err.Error())
	}

	// Scheduling unsubscribe, to be executed when end of this block is reached
	defer subs.Unsubscribe()

	// Flag to check for whether this is first time block header being received
	// or not
	//
	// If yes, we'll start syncer to fetch all block starting from 0 to this block
	first := true

	_lock.Lock()
	_synced.StartedAt = time.Now().UTC()
	_lock.Unlock()

	// Starting go routine for fetching blocks `ette` failed to process in previous attempt
	//
	// Uses Redis backed queue for fetching pending block hash & retries
	go retryBlockFetching(connection.RPC, _db, redisClient, redisKey, _lock, _synced)

	for {
		select {
		case err := <-subs.Err():
			log.Printf("[!] Block header subscription failed in mid : %s\n", err.Error())
			break
		case header := <-headerChan:
			if first {

				// If historical data query features are enabled
				// only then we need to sync to latest state of block chain
				if cfg.Get("EtteMode") == "1" || cfg.Get("EtteMode") == "3" {
					// Starting syncer in another thread, where it'll keep fetching
					// blocks starting from genesis to this block
					go SyncToLatestBlock(connection.RPC, _db, redisClient, redisKey, 0, header.Number.Uint64(), _lock, _synced)
					// Making sure on when next latest block header is received, it'll not
					// start another syncer
					first = false
				}

			}

			go fetchBlockByHash(connection.RPC, header.Hash(), _db, redisClient, redisKey, _lock, _synced)
		}
	}
}
