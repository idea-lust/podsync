package storage

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis"
	"github.com/mxpv/podsync/web/pkg/api"
	"github.com/pkg/errors"
)

const expiration = 24 * time.Hour * 90

// Backward compatible Redis storage for feeds
type RedisStorage struct {
	client *redis.Client
}

func (r *RedisStorage) parsePageSize(m map[string]string) (int, error) {
	str, ok := m["pagesize"]
	if !ok {
		return 50, nil
	}

	size, err := strconv.ParseInt(str, 10, 32)
	if err != nil {
		return 0, err
	}

	if size > 150 {
		return 0, errors.New("invalid page size")
	}

	return int(size), nil
}

func (r *RedisStorage) parseFormat(m map[string]string) (api.Format, api.Quality, error) {
	quality, ok := m["quality"]
	if !ok {
		return api.VideoFormat, api.HighQuality, nil
	}

	if quality == "VideoHigh" {
		return api.VideoFormat, api.HighQuality, nil
	} else if quality == "VideoLow" {
		return api.VideoFormat, api.LowQuality, nil
	} else if quality == "AudioHigh" {
		return api.AudioFormat, api.HighQuality, nil
	} else if quality == "AudioLow" {
		return api.AudioFormat, api.LowQuality, nil
	}

	return "", "", fmt.Errorf("unsupported formmat %s", quality)
}

func (r *RedisStorage) GetFeed(hashId string) (*api.Feed, error) {
	result, err := r.client.HGetAll(hashId).Result()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to query feed with id %s", hashId)
	}

	// Expire after 3 month if no use
	if err := r.client.Expire(hashId, expiration).Err(); err != nil {
		return nil, errors.Wrap(err, "failed query update feed")
	}

	feed := &api.Feed{
		PageSize: api.DefaultPageSize,
		Quality:  api.DefaultQuality,
		Format:   api.DefaultFormat,
	}

	m := make(map[string]string, len(result))
	for key, val := range result {
		m[strings.ToLower(key)] = val
	}

	j, ok := m["json"]
	if ok {
		if err := json.Unmarshal([]byte(j), feed); err != nil {
			return nil, err
		}

		return feed, nil
	}

	// Unpack provider and link type
	provider := m["provider"]
	linkType := m["type"]
	if strings.EqualFold(provider, "youtube") {
		feed.Provider = api.Youtube

		if strings.EqualFold(linkType, "channel") {
			feed.LinkType = api.Channel
		} else if strings.EqualFold(linkType, "playlist") {
			feed.LinkType = api.Playlist
		} else if strings.EqualFold(linkType, "user") {
			feed.LinkType = api.User
		} else {
			return nil, fmt.Errorf("unsupported yt link type %s", linkType)
		}

	} else if strings.EqualFold(provider, "vimeo") {
		feed.Provider = api.Vimeo

		if strings.EqualFold(linkType, "channel") {
			feed.LinkType = api.Channel
		} else if strings.EqualFold(linkType, "user") {
			feed.LinkType = api.User
		} else if strings.EqualFold(linkType, "group") {
			feed.LinkType = api.Group
		} else {
			return nil, fmt.Errorf("unsupported vimeo link type %s", linkType)
		}

	} else {
		return nil, errors.New("unsupported provider")
	}

	// Unpack item id
	id, ok := m["id"]
	if !ok || id == "" {
		return nil, errors.New("failed to unpack item id")
	}

	feed.ItemId = id

	// Fetch user id
	patreonId, ok := m["patreonid"]
	if ok {
		feed.UserId = patreonId
	}

	// Unpack page size
	pageSize, err := r.parsePageSize(m)
	if err != nil {
		return nil, err
	}

	if patreonId == "" && pageSize > 50 {
		return nil, errors.New("wrong feed data")
	}

	// Parse feed's format and quality
	format, quality, err := r.parseFormat(m)
	if err != nil {
		return nil, err
	}

	feed.Format = format
	quality = quality

	return feed, nil
}

func (r *RedisStorage) CreateFeed(feed *api.Feed) error {
	raw, err := json.Marshal(feed)
	if err != nil {
		return err
	}

	fields := map[string]interface{}{"json": string(raw)}
	if err := r.client.HMSet(feed.HashId, fields).Err(); err != nil {
		return errors.Wrap(err, "failed to save feed")
	}

	return r.client.Expire(feed.HashId, expiration).Err()
}

func (r *RedisStorage) keys() ([]string, error) {
	return r.client.Keys("*").Result()
}

func NewRedisStorage(redisUrl string) (*RedisStorage, error) {
	opts, err := redis.ParseURL(redisUrl)
	if err != nil {
		return nil, err
	}

	client := redis.NewClient(opts)
	return &RedisStorage{client}, nil
}
