package deploy

import (
	"encoding/json"
	"testing"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestRedisUsersACLRetainsOriginalBytes(t *testing.T) {
	const want = "user default on #27ef311678b9488450adf16ad298729bf9e58fbe0967680675e502f7016aab55 ~* &* +@all\n"
	if got := redisUsersACL("redis-acl-test-only"); got != want {
		t.Fatal("Redis ACL changed from the original Docker and native templates")
	}
}

func TestServiceJSONTemplatesPreserveCredentialEscaping(t *testing.T) {
	secret := "s<e&c>r\"et\\value\n"
	access := "a<\"ccess"
	var s3 struct {
		Identities []struct {
			Credentials []struct{ AccessKey, SecretKey string }
		}
	}
	if err := json.Unmarshal(seaweedS3Configuration(access, secret), &s3); err != nil {
		t.Fatal(err)
	}
	if len(s3.Identities) != 1 || len(s3.Identities[0].Credentials) != 1 || s3.Identities[0].Credentials[0].AccessKey != access || s3.Identities[0].Credentials[0].SecretKey != secret {
		t.Fatal("S3 credential changed during template rendering")
	}
	_, req := serviceRequestFixture(t)
	d, err := serviceinstall.Find("mediamtx", "1.21.2")
	if err != nil {
		t.Fatal(err)
	}
	req.Installation.Definition = d
	req.Installation.Config.Username = access
	for _, native := range []bool{false, true} {
		var media struct{ AuthInternalUsers []struct{ User, Pass string } }
		if err := json.Unmarshal(mediaMTXConfiguration(*req.Installation, secret, native), &media); err != nil {
			t.Fatal(err)
		}
		if len(media.AuthInternalUsers) != 1 || media.AuthInternalUsers[0].User != access || media.AuthInternalUsers[0].Pass != secret {
			t.Fatal("MediaMTX credential changed during template rendering")
		}
	}
}
