package buildinfo

import "testing"

func TestChannelContract(t *testing.T) {
	original := Channel
	t.Cleanup(func() { Channel = original })
	for _, tt := range []struct{ channel, instance, url, prefix, binary string }{
		{"live", "production", LiveURL, "JETSCALE_", "jetscale"},
		{"local", "local", LocalURL, "JETSCALE_LOCAL_", "jetscale-local"},
		{"unstable", "unstable", UnstableURL, "JETSCALE_UNSTABLE_", "jetscale-unstable"},
	} {
		t.Run(tt.channel, func(t *testing.T) {
			Channel = tt.channel
			if err := Validate(); err != nil {
				t.Fatal(err)
			}
			if DefaultInstance() != tt.instance || DefaultURL() != tt.url || EnvVar("API_URL") != tt.prefix+"API_URL" || BinaryName() != tt.binary {
				t.Fatalf("incorrect channel defaults for %s", Channel)
			}
		})
	}
	for _, channel := range []string{"", "production", "LOCAL", "../live"} {
		Channel = channel
		if Validate() == nil || DefaultURL() != "" || DefaultInstance() != "" {
			t.Fatalf("invalid channel %q must not fall back to live", channel)
		}
	}
}
