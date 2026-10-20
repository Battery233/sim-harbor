package config

import "testing"

func TestVoicemailURLRequiresPrivateHTTP(t *testing.T) {
	token := "0123456789abcdef0123456789abcdef"
	ok := "http://192.168.31.10:18880/api/voicemail/amr/" + token + "?slot={{slot}}&start={{telStartTs}}&phone={{phNum|$urlEncode()}}"
	c := valid()
	c.VoicemailEnabled = true
	c.VoicemailSlots = []int{2, 1}
	c.VoicemailTTS = "请在提示音后留言"
	c.VoicemailAMRURL = ok
	if err := c.Validate(); err != nil || c.VoicemailSlots[0] != 1 || c.VoicemailSlots[1] != 2 {
		t.Fatal(err)
	}
	if got, yes := VoicemailUploadToken(ok); !yes || got != token {
		t.Fatal(got, yes)
	}
	for _, bad := range []string{
		"http://lab.example.nip.io:18880/api/voicemail/amr/" + token,
		"https://192.168.31.10:18880/api/voicemail/amr/" + token,
		"http://203.0.113.5:18880/api/voicemail/amr/" + token,
		"http://192.168.31.10/api/voicemail/amr/" + token,
		"http://192.168.31.10:18880/api/voicemail/amr/short",
		"clear",
		"http://user:secret@192.168.31.10:18880/api/voicemail/amr/" + token,
	} {
		c := valid()
		c.VoicemailAMRURL = bad
		if c.Validate() == nil {
			t.Fatal("accepted", bad)
		}
	}
	off := valid()
	if err := off.Validate(); err != nil || off.VoicemailEnabled {
		t.Fatal("default voicemail must stay off", err)
	}
}
