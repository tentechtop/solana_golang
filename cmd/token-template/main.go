// token-template emits the existing permissionless fungible asset template for wallet clients.
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"

	vmprogram "github.com/tentechtop/ordexus-core/programs/vm"
)

func main() {
	kind := flag.String("kind", "token", "token or fundraiser")
	flag.Parse()
	var data []byte
	var err error
	switch *kind {
	case "token":
		data, err = vmprogram.ERC20LikeContractProgramData()
	case "fundraiser":
		data, err = vmprogram.FundraiserProgramData()
	case "market":
		data, err = vmprogram.MarketProgramData()
	default:
		panic("unsupported template kind")
	}
	if err != nil {
		panic(err)
	}
	hash := sha256.Sum256(data)
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"version": 1, "template_base64": base64.StdEncoding.EncodeToString(data), "template_hash": hex.EncodeToString(hash[:]),
	}); err != nil {
		panic(err)
	}
}
