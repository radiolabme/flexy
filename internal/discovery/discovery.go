// Package discovery wraps flexclient.DiscoverAll with a typed Radio struct
// and context-aware scanning.
package discovery

import (
	"context"
	"strings"
	"time"

	"github.com/kc2g-flex-tools/flexclient"
)

// Radio represents a discovered FlexRadio.
type Radio struct {
	Serial   string
	Nickname string
	Model    string
	IP       string
	Port     string
	Version  string
	Status   string
	Inuse    string
	Stations []string
	Raw      map[string]string
}

func radioFromKV(kv map[string]string) Radio {
	r := Radio{
		Serial:   kv["serial"],
		Nickname: kv["nickname"],
		Model:    kv["model"],
		IP:       kv["ip"],
		Port:     kv["port"],
		Version:  kv["version"],
		Status:   kv["status"],
		Inuse:    kv["inuse_host"],
		Raw:      kv,
	}
	if s := kv["gui_client_stations"]; s != "" {
		for _, name := range strings.Split(s, ",") {
			if name = strings.TrimSpace(name); name != "" {
				r.Stations = append(r.Stations, name)
			}
		}
	}
	return r
}

// Scan listens for discovery broadcasts and sends updated radio lists to the
// returned channel. The channel is closed when ctx is cancelled.
func Scan(ctx context.Context, expiry time.Duration) <-chan []Radio {
	out := make(chan []Radio, 1)
	go func() {
		defer close(out)
		raw := make(chan []map[string]string, 1)
		go flexclient.DiscoverAll(ctx, expiry, raw)
		for {
			select {
			case <-ctx.Done():
				return
			case batch, ok := <-raw:
				if !ok {
					return
				}
				radios := make([]Radio, 0, len(batch))
				for _, kv := range batch {
					radios = append(radios, radioFromKV(kv))
				}
				select {
				case out <- radios:
				default:
					select {
					case <-out:
					default:
					}
					out <- radios
				}
			}
		}
	}()
	return out
}

// FindOne waits until at least one radio is discovered, then returns it.
func FindOne(ctx context.Context, timeout time.Duration) []Radio {
	ch := Scan(ctx, timeout)
	for {
		select {
		case <-ctx.Done():
			return nil
		case radios, ok := <-ch:
			if !ok {
				return nil
			}
			if len(radios) > 0 {
				return radios
			}
		}
	}
}
