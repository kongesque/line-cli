package line

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestGetLastOpRevisionContextCancellation(t *testing.T) {
	testRPCContextCancellation(t, "getLastOpRevision", func(client *Client, ctx context.Context) error {
		_, err := client.GetLastOpRevisionContext(ctx)
		return err
	})
}

func TestGetProfileContextCancellation(t *testing.T) {
	testRPCContextCancellation(t, "getProfile", func(client *Client, ctx context.Context) error {
		_, err := client.GetProfileContext(ctx)
		return err
	})
}

func TestGetEncryptedIdentityV3ContextCancellation(t *testing.T) {
	testRPCContextCancellation(t, "getEncryptedIdentityV3", func(client *Client, ctx context.Context) error {
		_, err := client.GetEncryptedIdentityV3Context(ctx)
		return err
	})
}

func TestCommandRPCsPropagateCancellation(t *testing.T) {
	calls := map[string]func(*Client, context.Context) error{
		"contacts": func(c *Client, ctx context.Context) error { _, e := c.GetAllContactIdsContext(ctx); return e },
		"contact names": func(c *Client, ctx context.Context) error {
			_, e := c.GetContactsV2Context(ctx, []string{"u-peer"})
			return e
		},
		"boxes": func(c *Client, ctx context.Context) error {
			_, e := c.GetMessageBoxesContext(ctx, MessageBoxesOptions{})
			return e
		},
		"history": func(c *Client, ctx context.Context) error {
			_, e := c.GetRecentMessagesV2Context(ctx, "u-peer", 10)
			return e
		},
		"blocked": func(c *Client, ctx context.Context) error { _, e := c.GetBlockedContactIdsContext(ctx); return e },
		"negotiate": func(c *Client, ctx context.Context) error {
			_, e := c.NegotiateE2EEPublicKeyContext(ctx, "u-peer")
			return e
		},
		"peer key": func(c *Client, ctx context.Context) error {
			_, e := c.GetE2EEPublicKeyContext(ctx, "u-peer", 1, 1)
			return e
		},
		"group key": func(c *Client, ctx context.Context) error {
			_, e := c.GetE2EEGroupSharedKeyContext(ctx, "c-group", 1)
			return e
		},
		"last group key": func(c *Client, ctx context.Context) error {
			_, e := c.GetLastE2EEGroupSharedKeyContext(ctx, "c-group")
			return e
		},
		"members": func(c *Client, ctx context.Context) error {
			_, e := c.GetChatsContext(ctx, []string{"c-group"}, true, true)
			return e
		},
		"register": func(c *Client, ctx context.Context) error {
			return c.RegisterE2EEGroupKeyContext(ctx, 1, "c-group", nil, nil, nil)
		},
		"send": func(c *Client, ctx context.Context) error {
			_, e := c.SendMessageContext(ctx, 1, &Message{To: "u-peer", Text: "synthetic"})
			return e
		},
		"react": func(c *Client, ctx context.Context) error {
			return c.ReactContext(ctx, 1, "123", ReactionType{PredefinedReactionType: 2})
		},
		"remove reaction": func(c *Client, ctx context.Context) error { return c.CancelReactionContext(ctx, 1, "123") },
		"unsend":          func(c *Client, ctx context.Context) error { return c.UnsendMessageContext(ctx, 1, "123") },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			testRPCContextCancellation(t, name, call)
			client := NewClient("synthetic")
			client.HTTPClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("canceled request reached transport")
				return nil, nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := call(client, ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestOBSTokenPreparationCancellation(t *testing.T) {
	obsTokenMu.Lock()
	old, expiry := obsTokenCache, obsTokenExpiry
	obsTokenCache = ""
	obsTokenMu.Unlock()
	defer func() { obsTokenMu.Lock(); obsTokenCache, obsTokenExpiry = old, expiry; obsTokenMu.Unlock() }()
	for _, name := range []string{"upload", "plain upload", "download"} {
		t.Run(name, func(t *testing.T) {
			testRPCContextCancellation(t, name, func(c *Client, ctx context.Context) error {
				c.OBSClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					t.Fatal("transfer after token cancellation")
					return nil, nil
				})}
				switch name {
				case "upload":
					_, e := c.UploadOBSWithSIDContext(ctx, []byte("synthetic"), "emf")
					return e
				case "plain upload":
					return c.UploadOBSPlainContext(ctx, []byte("synthetic"), "123", "file")
				default:
					_, e := c.DownloadOBSWithSIDOptions(ctx, "123", "", "m", OBSDownloadOptions{})
					return e
				}
			})
		})
	}
}

func TestOBSUploadsCancelInFlight(t *testing.T) {
	obsTokenMu.Lock()
	old, expiry := obsTokenCache, obsTokenExpiry
	obsTokenCache, obsTokenExpiry = "synthetic", time.Now().Add(time.Hour)
	obsTokenMu.Unlock()
	defer func() { obsTokenMu.Lock(); obsTokenCache, obsTokenExpiry = old, expiry; obsTokenMu.Unlock() }()
	for _, plain := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		started := make(chan struct{})
		done := make(chan error, 1)
		c := NewClient("synthetic")
		c.OBSClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			close(started)
			<-r.Context().Done()
			return nil, r.Context().Err()
		})}
		go func() {
			if plain {
				done <- c.UploadOBSPlainContext(ctx, nil, "123", "file")
			} else {
				_, e := c.UploadOBSWithSIDContext(ctx, nil, "emf")
				done <- e
			}
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("upload did not start")
		}
		cancel()
		select {
		case e := <-done:
			if !errors.Is(e, context.Canceled) {
				t.Fatal(e)
			}
		case <-time.After(time.Second):
			t.Fatal("upload did not cancel")
		}
	}
}
