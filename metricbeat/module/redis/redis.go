// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

/*
Package redis contains shared Redis functionality for the metric sets
*/
package redis

import (
	"strings"
	"time"

	rd "github.com/gomodule/redigo/redis"

	"github.com/elastic/elastic-agent-libs/logp"
)

// Redis types
const (
	TypeNone      = "none"
	TypeString    = "string"
	TypeList      = "list"
	TypeSet       = "set"
	TypeSortedSet = "zset"
	TypeHash      = "hash"
)

// ParseRedisInfo parses the string returned by the INFO command
// Every line is split up into key and value
func ParseRedisInfo(info string) map[string]string {
	// Feed every line into
	result := strings.Split(info, "\r\n")

	// Load redis info values into array
	values := map[string]string{}

	for _, value := range result {
		// Values are separated by :
		parts := ParseRedisLine(value, ":")
		if len(parts) == 2 {
			if strings.Contains(parts[0], "cmdstat_") {
				cmdstats := ParseRedisCommandStats(parts[0], parts[1])
				for k, v := range cmdstats {
					key := parts[0] + "_" + k
					values[key] = v
				}
			} else {
				values[parts[0]] = parts[1]
			}
		}
	}
	return values
}

// ParseRedisLine parses a single line returned by INFO
func ParseRedisLine(s, delimiter string) []string {
	return strings.Split(s, delimiter)
}

// ParseRedisCommandStats parses a map of stats returned by INFO COMMANDSTATS
func ParseRedisCommandStats(key, s string) map[string]string {
	// calls=XX,usec=XXX,usec_per_call=XXX
	results := strings.Split(s, ",")

	values := map[string]string{}

	for _, value := range results {
		parts := strings.Split(value, "=")
		if len(parts) == 2 {
			values[parts[0]] = parts[1]
		}
	}
	return values
}

// FetchRedisInfo returns a map of requested stats.
func FetchRedisInfo(stat string, c rd.Conn, logger *logp.Logger) (map[string]string, error) {
	out, err := rd.String(c.Do("INFO", stat))
	if err != nil {
		logger.Errorf("Error retrieving INFO stats: %v", err)
		return nil, err
	}
	return ParseRedisInfo(out), nil
}

// FetchSlowLogLength returns count of slow operations
func FetchSlowLogLength(c rd.Conn, logger *logp.Logger) (int64, error) {
	count, err := rd.Int64(c.Do("SLOWLOG", "len"))
	if err != nil {
		logger.Errorf("Error retrieving slowlog len: %v", err)
		return 0, err
	}

	return count, nil
}

// FetchKeyInfo collects info about a key
func FetchKeyInfo(c rd.Conn, key string, logger *logp.Logger) (map[string]interface{}, error) {
	keyType, err := rd.String(c.Do("TYPE", key))
	if err != nil {
		return nil, err
	}
	if keyType == TypeNone {
		// Ignore it, it has been removed
		return nil, nil
	}

	keyTTL, err := rd.Int64(c.Do("TTL", key))
	if err != nil {
		return nil, err
	}

	info := map[string]interface{}{
		"name": key,
		"type": keyType,
		"expire": map[string]interface{}{
			"ttl": keyTTL,
		},
	}

	lenCommand := ""

	switch keyType {
	case TypeString:
		lenCommand = "STRLEN"
	case TypeList:
		lenCommand = "LLEN"
	case TypeSet:
		lenCommand = "SCARD"
	case TypeSortedSet:
		lenCommand = "ZCARD"
	case TypeHash:
		lenCommand = "HLEN"
	default:
		logger.Named("redis").Debugf("Not supported length for type %s", keyType)
	}

	if lenCommand != "" {
		length, err := rd.Int64(c.Do(lenCommand, key))
		if err != nil {
			return nil, err
		}
		info["length"] = length
	}

	return info, nil
}

// FetchKeys gets a list of keys based on a pattern using SCAN, `limit` is a
// safeguard to limit the number of commands executed and the number of keys
// returned, if more than `limit` keys are being collected the method stops
// and returns the keys already collected. Setting `limit` to ' disables this
// limit.
func FetchKeys(c rd.Conn, pattern string, limit uint) ([]string, error) {
	cursor := 0
	var keys []string
	for {
		resp, err := rd.Values(c.Do("SCAN", cursor, "MATCH", pattern))
		if err != nil {
			return nil, err
		}

		var scanKeys []string
		_, err = rd.Scan(resp, &cursor, &scanKeys)
		if err != nil {
			return nil, err
		}

		keys = append(keys, scanKeys...)

		if cursor == 0 || (limit > 0 && len(keys) > int(limit)) {
			break
		}
	}
	return keys, nil
}

// Select selects the keyspace to use for this connection
func Select(c rd.Conn, keyspace uint) error {
	_, err := c.Do("SELECT", keyspace)
	return err
}

// Pool is a redis pool that keeps track of the database number originally configured
type Pool struct {
	*rd.Pool

	dbNumber int
}

// DBNumber returns the db number originally used to configure this pool
func (p *Pool) DBNumber() int {
	return p.dbNumber
}

// CreatePool creates a redis connection pool
func CreatePool(host, username, password string, dbNumber int, config *Config, connTimeout time.Duration) *Pool {
	pool := &rd.Pool{
		MaxIdle:     config.MaxConn,
		IdleTimeout: config.IdleTimeout,
		Dial: func() (rd.Conn, error) {
			dialOptions := []rd.DialOption{
				rd.DialUsername(username),
				rd.DialPassword(password),
				rd.DialDatabase(dbNumber),
				rd.DialConnectTimeout(connTimeout),
				rd.DialReadTimeout(connTimeout),
				rd.DialWriteTimeout(connTimeout),
			}

			if config.TLS.IsEnabled() {
				dialOptions = append(dialOptions,
					rd.DialUseTLS(true),
					rd.DialTLSConfig(config.UseTLSConfig),
				)
			}

			return rd.Dial(config.Network, host, dialOptions...)
		},
	}

	return &Pool{Pool: pool, dbNumber: dbNumber}
}
