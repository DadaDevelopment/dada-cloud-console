package isolation

import (
	"encoding/base64"
	"encoding/json"
)

// dockerConfigJSON renders a .dockerconfigjson body for one registry host so
// BuildKit can authenticate its push to Harbor with a robot account.
func dockerConfigJSON(host, user, pass string) []byte {
	auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
	cfg := map[string]any{
		"auths": map[string]any{
			host: map[string]string{
				"username": user,
				"password": pass,
				"auth":     auth,
			},
		},
	}
	b, _ := json.Marshal(cfg)
	return b
}
