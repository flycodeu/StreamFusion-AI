package serviceinstall

import "testing"

func TestNodeRegistryURLValidation(t *testing.T) {
	for _, value := range []string{"https://registry.npmjs.org/", "https://registry.npmmirror.com/", "http://10.0.0.2:4873/repository/npm/", "https://[::1]:443/npm/"} {
		if err := ValidateNodeRegistryURL(value); err != nil {
			t.Fatalf("valid URL rejected: %s", value)
		}
	}
	for _, value := range []string{"", "file:///tmp/npm", "https://user:secret@npm.test/", "https://npm.test/?token=secret", "https://npm.test/#secret", "https://npm.test/${TOKEN}", "https://npm.test/%24%7BTOKEN%7D", "https://npm.test:0/", "https://npm.test:65536/", "https://npm.test:/", "https://npm.test/%0afoo", "https://npm.test/\n", " https://npm.test/", "https://npm.test/?"} {
		if ValidateNodeRegistryURL(value) == nil {
			t.Fatalf("unsafe URL accepted: %q", value)
		}
	}
}
