// rwa-template emits the immutable regulated RWA template consumed by clients and Ordexus Studio.
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"

	vmprogram "github.com/tentechtop/ordexus-core/programs/vm"
)

func main() {
	data, err := vmprogram.RWAProgramData()
	if err != nil {
		panic(err)
	}
	hash := sha256.Sum256(data)
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"version":         1,
		"template_base64": base64.StdEncoding.EncodeToString(data),
		"template_hash":   hex.EncodeToString(hash[:]),
	}); err != nil {
		panic(err)
	}
}
