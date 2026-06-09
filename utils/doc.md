# utils 工具包介绍与使用文档

`utils` 是当前项目中的 Solana/加密工具包，包名为 `solana_golang/utils`。它提供 Base58、Hex/Base64、字节序转换、哈希、Ed25519、Solana keypair、BIP-39/SLIP-0010 HD 钱包、X25519 + AES-GCM、PDA、short_vec 和 P2P multi-address 等常用能力。

## 本次测试结果

已在项目根目录 `F:\workSpace2029\solana_golang` 重新执行工具包测试：

```powershell
go test -count=1 -v ./utils
```

结果：全部通过。测试覆盖普通单元测试和 fuzz seed 用例，包括 Base58、Hex/整数/哈希、Ed25519、X25519 + AES-GCM、BIP-39、SLIP-0010、Solana keypair、multi-address、PDA、short_vec 等模块。

另外尝试执行 race 检查：

```powershell
go test -race -count=1 ./utils
```

当前 Windows 环境未完成 race 检查，原因是 `-race` 需要 cgo；临时启用 `CGO_ENABLED=1` 后，本机 `%PATH%` 中缺少 `gcc`。如需运行 race 检查，请先安装 MinGW-w64 或其他可用 C 编译器，并确保 `gcc` 可被命令行找到。

## 导入方式

在当前 module 内使用：

```go
import "solana_golang/utils"
```

如果后续 module path 调整，请同步替换 import 路径。

## 功能概览

| 文件 | 主要能力 |
| --- | --- |
| `base58.go` | Bitcoin/Solana Base58 编解码 |
| `bytes.go` | Hex、大小端整数转换、SHA-256/SHA-512、HMAC-SHA512、字节复制/拼接/反转 |
| `crypto.go` | 安全随机数、常量时间比较、Base64/Base64 raw |
| `public_key.go` | Solana `PublicKey`、`Hash`、`Signature` 固定长度类型 |
| `ed25519.go` | Ed25519 keypair、签名、验签和调用别名 |
| `solana_keypair.go` | Solana seed keypair、64 字节 secret key 格式、签名验签 |
| `bip39.go` | BIP-39 英文助记词、entropy、seed |
| `hd_wallet.go` | SLIP-0010 Ed25519 HD 钱包和 Solana 派生路径 |
| `ecc_with_aes_gcm.go` | X25519 共享密钥、HKDF-SHA256、AES-256-GCM 加解密 |
| `pda.go` | Solana PDA、bump seed、create_with_seed、Ed25519 curve 检查 |
| `shortvec.go` | Solana short_vec compact-u16 长度编解码 |
| `multi_address.go` | `/ip4/.../tcp|quic/.../p2p/...` 地址解析和构建 |

## 常用示例

### Base58 编解码

```go
package main

import (
	"fmt"
	"solana_golang/utils"
)

func main() {
	raw := []byte("hello solana")

	encoded := utils.Base58Encode(raw)
	decoded, err := utils.Base58Decode(encoded)
	if err != nil {
		panic(err)
	}

	fmt.Println(encoded)
	fmt.Println(string(decoded))
}
```

### Hex、哈希和字节工具

```go
data := []byte("hello")

hexText := utils.BytesToHexWithPrefix(data)
decoded, err := utils.HexToBytes(hexText)
if err != nil {
	panic(err)
}

sha256Hex := utils.SHA256Hex(decoded)
checksum := utils.Checksum4(decoded)

fmt.Println(hexText, sha256Hex, checksum)
```

常用函数：

| 分类 | 函数 |
| --- | --- |
| Hex | `BytesToHex`、`BytesToHexWithPrefix`、`HexToBytes`、`NormalizeHex`、`IsHexString` |
| 大端整数 | `IntToBytes`、`BytesToInt`、`Int32ToBytes`、`BytesToInt32`、`Int64ToBytes`、`BytesToInt64`、`Uint16ToBytes`、`BytesToUint16`、`Uint32ToBytes`、`BytesToUint32`、`Uint64ToBytes`、`BytesToUint64` |
| 小端整数 | `Uint16ToBytesLE`、`BytesToUint16LE`、`Uint32ToBytesLE`、`BytesToUint32LE`、`Uint64ToBytesLE`、`BytesToUint64LE`、`Int16ToBytesLE`、`BytesToInt16LE`、`Int32ToBytesLE`、`BytesToInt32LE`、`Int64ToBytesLE`、`BytesToInt64LE` |
| 哈希 | `SHA256`、`SHA256Hex`、`DoubleSHA256`、`DoubleSHA256Hex`、`Checksum4`、`SHA512`、`SHA512Hex`、`HMACSHA512` |
| 字节切片 | `CloneBytes`、`ConcatBytes`、`ReverseBytes` |

### PublicKey、Hash 和 Signature

```go
publicKey, err := utils.PublicKeyFromBase58("11111111111111111111111111111111")
if err != nil {
	panic(err)
}

fmt.Println(publicKey.String()) // Base58
fmt.Println(publicKey.Hex())    // hex
fmt.Println(publicKey.Bytes())  // copy of raw bytes
```

约束：

| 类型 | 长度 |
| --- | --- |
| `PublicKey` | 32 字节 |
| `Hash` / `Blockhash` | 32 字节 |
| `Signature` | 64 字节 |

### Ed25519 签名验签

```go
keyPair, err := utils.GenerateEd25519KeyPair()
if err != nil {
	panic(err)
}

message := []byte("hello")
signature, err := utils.Ed25519Sign(keyPair.PrivateKey, message)
if err != nil {
	panic(err)
}

ok := utils.Ed25519Verify(keyPair.PublicKey, message, signature)
fmt.Println(ok)
```

注意：本工具包中的 Ed25519 `PrivateKey` 是 32 字节 seed，不是 Go 标准库 `ed25519.PrivateKey` 的 64 字节完整私钥。

### Solana KeyPair

```go
seed, err := utils.RandomBytes(utils.SolanaPrivateKeySeedSize)
if err != nil {
	panic(err)
}

keyPair, err := utils.KeyPairFromSeed(seed)
if err != nil {
	panic(err)
}

secretKey64 := keyPair.SecretKey64()
loaded, err := utils.KeyPairFromSecretKey64(secretKey64)
if err != nil {
	panic(err)
}

message := []byte("solana")
sig, err := loaded.Sign(message)
if err != nil {
	panic(err)
}

fmt.Println(loaded.PublicKey.String())
fmt.Println(loaded.Verify(message, sig))
```

约束：

| 常量 | 含义 |
| --- | --- |
| `SolanaPrivateKeySeedSize` | 32 字节 Ed25519 seed |
| `SolanaSecretKeySize` | 64 字节 Solana CLI secret key，格式为 `seed + publicKey` |

### BIP-39 助记词和 Solana HD 钱包

```go
mnemonic, err := utils.GenerateMnemonicString()
if err != nil {
	panic(err)
}

seed, err := utils.GenerateSeedFromMnemonicString(mnemonic, "")
if err != nil {
	panic(err)
}

info, err := utils.GetSolanaKeyPairFromSeed(seed, 0, 0)
if err != nil {
	panic(err)
}

fmt.Println(mnemonic)
fmt.Println(info.Path)    // m/44'/501'/0'/0'
fmt.Println(info.Address) // Base58 public key
```

也可以使用指定路径：

```go
info, err := utils.GetSolanaKeyPairFromSeedAndPath(seed, "m/44'/501'/0'/0'")
if err != nil {
	panic(err)
}
fmt.Println(info.Address)
```

常用函数：

| 分类 | 函数 |
| --- | --- |
| BIP-39 entropy | `NewBIP39Entropy`、`NewBIP39Mnemonic`、`EntropyFromBIP39Mnemonic` |
| BIP-39 seed | `IsBIP39MnemonicValid`、`NewBIP39Seed`、`GenerateSeed`、`GenerateSeedFromMnemonicString` |
| 助记词生成 | `GenerateMnemonic`、`GenerateMnemonicString` |
| SLIP-0010 | `GenerateRootHDNode`、`GenerateRootPrivateKey`、`DeriveChildHDNode`、`DeriveChildPrivateKey` |
| Solana 派生 | `GetSolanaKeyPair`、`GetSolanaKeyPairFromMnemonicString`、`GetSolanaKeyPairFromSeed`、`GetSolanaKeyPairFromSeedAndPath`、`DerivePrivateKeyFromSeedAndPath`、`DeriveHDNodeFromSeedAndPath` |
| 路径 | `ParseDerivationPath`、`SolanaDerivationPath` |

关键约束：

| 项 | 约束 |
| --- | --- |
| BIP-39 entropy | 128 到 256 bit，且必须是 32 的倍数 |
| `GenerateMnemonicString` | 默认生成 12 个英文单词 |
| BIP-39 seed | 64 字节 |
| SLIP-0010 seed | 16 到 64 字节 |
| Ed25519 HD child | 只支持 hardened index |
| Solana 标准路径 | `m/44'/501'/accountIndex'/addressIndex'` |

### X25519 + AES-256-GCM 加解密

```go
alice, err := utils.GenerateCurve25519KeyPair()
if err != nil {
	panic(err)
}
bob, err := utils.GenerateCurve25519KeyPair()
if err != nil {
	panic(err)
}

aliceSecret, err := utils.GenerateSharedSecret(alice.PrivateKey, bob.PublicKey)
if err != nil {
	panic(err)
}
bobSecret, err := utils.GenerateSharedSecret(bob.PrivateKey, alice.PublicKey)
if err != nil {
	panic(err)
}

salt, err := utils.RandomBytes(utils.AESGCMHKDFSaltSize)
if err != nil {
	panic(err)
}
aliceKey, err := utils.DeriveAESKeyWithSalt(aliceSecret, salt)
if err != nil {
	panic(err)
}
bobKey, err := utils.DeriveAESKeyWithSalt(bobSecret, salt)
if err != nil {
	panic(err)
}

encrypted, err := utils.AESGCMEncrypt(aliceKey, []byte("secret message"))
if err != nil {
	panic(err)
}
plain, err := utils.AESGCMDecrypt(bobKey, encrypted)
if err != nil {
	panic(err)
}

fmt.Println(string(plain))
```

注意：`DeriveAESKey` 使用进程级随机 salt，适合同一进程内快速使用。跨进程、跨语言或需要双方派生相同密钥时，请使用 `DeriveAESKeyWithSalt` 并显式交换或保存 salt。

AES-GCM 密文格式为：

```text
nonce(12 bytes) + ciphertext + tag(16 bytes)
```

### PDA

```go
programID := utils.MustPublicKeyFromBase58("11111111111111111111111111111111")

pda, bump, err := utils.FindProgramAddress(
	[][]byte{[]byte("vault"), []byte("user-1")},
	programID,
)
if err != nil {
	panic(err)
}

fmt.Println(pda.String(), bump)
```

常用函数：

| 函数 | 说明 |
| --- | --- |
| `CreateProgramAddress` | 根据 seeds 和 program id 生成 PDA，要求结果不在 Ed25519 curve 上 |
| `FindProgramAddress` | 从 bump 255 到 0 查找第一个可用 PDA |
| `CreateWithSeed` | 实现 Solana `create_with_seed` |
| `IsOnEd25519Curve` | 判断 32 字节压缩点是否在 Ed25519 curve 上 |

关键约束：

| 项 | 约束 |
| --- | --- |
| 单个 seed | 最多 32 字节 |
| seeds 数量 | `CreateProgramAddress` 最多 16 个 |
| `FindProgramAddress` | 传入 seeds 最多 15 个，需要给 bump seed 留位置 |
| `CreateWithSeed` seed 字符串 | 最多 32 字节 |

### Solana short_vec 长度

```go
encoded, err := utils.EncodeShortVecLength(128)
if err != nil {
	panic(err)
}

length, bytesRead, err := utils.DecodeShortVecLength(encoded)
if err != nil {
	panic(err)
}

fmt.Println(length, bytesRead)
```

`short_vec` 当前实现为 compact-u16，支持范围为 `0` 到 `0xffff`。

### P2P MultiAddress

```go
peerBytes, err := utils.RandomBytes(utils.PublicKeySize)
if err != nil {
	panic(err)
}
peerID := utils.Base58Encode(peerBytes)

address, err := utils.BuildMultiAddress(
	utils.MultiAddressIP4,
	"127.0.0.1",
	utils.ProtocolTCP,
	5002,
	peerID,
)
if err != nil {
	panic(err)
}

parsed, err := utils.ParseMultiAddress(address.String())
if err != nil {
	panic(err)
}

fmt.Println(parsed.RawAddress)
```

支持的格式：

```text
/ip4/127.0.0.1/tcp/5002/p2p/<Base58Encoded32BytePeerID>
/ip4/127.0.0.1/quic/5002/p2p/<Base58Encoded32BytePeerID>
```

约束：

| 项 | 约束 |
| --- | --- |
| IP 类型 | `ip4` 或 `ip6` |
| 协议 | `tcp`、`udp`、`quic` |
| 端口 | 1 到 65535 |
| peer id | Base58 解码后必须是 32 字节 |

## 错误处理建议

大多数 API 返回 `(value, error)`，使用时应检查错误：

```go
value, err := utils.HexToBytes("0xdeadbeef")
if err != nil {
	return err
}
_ = value
```

只有 `MustPublicKeyFromBase58` 和 `MustEncodeShortVecLength` 会在失败时 panic，建议只用于测试、常量初始化或已经确定输入合法的场景。

## 安全注意事项

- 不要打印、日志记录或明文保存助记词、seed、私钥、chain code、shared secret、AES key。
- 跨进程或跨语言派生 AES key 时，优先使用 `DeriveAESKeyWithSalt`，并确保双方使用相同 salt。
- 比较密钥、哈希或签名等敏感字节时，优先使用 `SecureEqual`。
- `SolanaKeyPair.PrivateKey` 和 `Ed25519KeyPair.PrivateKey` 都是 32 字节 seed；如果需要 Solana CLI 的 64 字节 secret key，请使用 `SecretKey64` 或 `ToSecretKey64`。
- 使用 HD 钱包时请确认派生路径，默认 Solana 路径为 `m/44'/501'/accountIndex'/addressIndex'`。

## 测试命令

普通测试：

```powershell
go test ./utils
```

强制重新执行并输出详细用例：

```powershell
go test -count=1 -v ./utils
```

可选 race 检查：

```powershell
$env:CGO_ENABLED='1'
go test -race -count=1 ./utils
```

在 Windows 上运行 race 检查前，需要确保本机已经安装可用 C 编译器，例如 MinGW-w64，并且 `gcc` 在 `%PATH%` 中。
