package netease

import (
	"context"
	"testing"
)

func TestLiveCreateQRCode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live network test in short mode")
	}
	client, err := New("", nil)
	if err != nil {
		t.Fatalf("New err: %v", err)
	}

	qr, err := client.CreateQRCode(context.Background())
	if err != nil {
		t.Fatalf("CreateQRCode err: %v", err)
	}
	t.Logf("Key: %s", qr.Key)
	t.Logf("URL: %s", qr.URL)
	t.Logf("ImageData len: %d", len(qr.ImageData))

	status, _, err := client.CheckQRCode(context.Background(), qr.Key)
	if err != nil {
		t.Fatalf("CheckQRCode err: %v", err)
	}
	t.Logf("CheckQRCode status: %s", status)
}
