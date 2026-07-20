module github.com/Gealber/epochfill

go 1.25.7

// yellowstone-faithful pins forks of these three via its own replace directives.
// Those are IGNORED when the module is imported, so they must be repeated here or
// the build fails resolving github.com/anjor/carlet.
replace github.com/anjor/carlet => github.com/rpcpool/carlet v0.0.4

replace github.com/ipfs/go-cid => github.com/rpcpool/go-cid v0.5.1

replace github.com/mr-tron/base58 => github.com/rpcpool/base58 v1.2.1

require (
	github.com/gagliardetto/binary v0.8.0
	github.com/gagliardetto/solana-go v1.22.0
	github.com/glebarez/go-sqlite v1.22.0
	github.com/ipfs/go-cid v0.6.1
	github.com/ipld/go-ipld-prime v0.24.0
	github.com/joho/godotenv v1.5.1
	github.com/klauspost/compress v1.19.1
	github.com/multiformats/go-multihash v0.2.3
	github.com/rpcpool/yellowstone-faithful v0.7.24
	github.com/rs/zerolog v1.35.1
)

require (
	github.com/PuerkitoBio/purell v1.1.1 // indirect
	github.com/PuerkitoBio/urlesc v0.0.0-20170810143723-de5bf2ad4578 // indirect
	github.com/anjor/carlet v0.0.0-00010101000000-000000000000 // indirect
	github.com/benbjohnson/clock v1.3.5 // indirect
	github.com/blendle/zapdriver v1.3.1 // indirect
	github.com/bytedance/gopkg v0.1.3 // indirect
	github.com/bytedance/sonic v1.15.0 // indirect
	github.com/bytedance/sonic/loader v0.5.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cloudwego/base64x v0.1.6 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.2.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/fatih/color v1.18.0 // indirect
	github.com/filecoin-project/go-address v1.1.0 // indirect
	github.com/filecoin-project/go-fil-commcid v0.1.0 // indirect
	github.com/filecoin-project/go-fil-commp-hashhash v0.2.0 // indirect
	github.com/fxamacker/cbor/v2 v2.8.0 // indirect
	github.com/gagliardetto/treeout v0.1.4 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/goccy/go-json v0.10.6 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/goware/urlx v0.3.2 // indirect
	github.com/ipfs/go-block-format v0.2.0 // indirect
	github.com/ipfs/go-ipfs-util v0.0.3 // indirect
	github.com/ipfs/go-ipld-cbor v0.1.0 // indirect
	github.com/ipfs/go-ipld-format v0.6.0 // indirect
	github.com/jellydator/ttlcache/v3 v3.1.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/cpuid/v2 v2.2.9 // indirect
	github.com/libp2p/go-buffer-pool v0.1.0 // indirect
	github.com/libp2p/go-libp2p v0.32.1 // indirect
	github.com/logrusorgru/aurora v2.0.3+incompatible // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/minio/blake2b-simd v0.0.0-20160723061019-3f5f724cb5b1 // indirect
	github.com/minio/sha256-simd v1.0.1 // indirect
	github.com/mitchellh/go-testing-interface v1.14.1 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/mostynb/zstdpool-freelist v0.0.0-20201229113212-927304c0c3b1 // indirect
	github.com/mr-tron/base58 v1.3.0 // indirect
	github.com/multiformats/go-base32 v0.1.0 // indirect
	github.com/multiformats/go-base36 v0.2.0 // indirect
	github.com/multiformats/go-multiaddr v0.12.0 // indirect
	github.com/multiformats/go-multibase v0.3.0 // indirect
	github.com/multiformats/go-multicodec v0.10.0 // indirect
	github.com/multiformats/go-varint v0.1.0 // indirect
	github.com/novifinancial/serde-reflection/serde-generate/runtime/golang v0.0.0-20220519162058-e5cd3c3b3f3a // indirect
	github.com/oasisprotocol/curve25519-voi v0.0.0-20251114093237-2ab5a27a1729 // indirect
	github.com/polydawn/refmt v0.90.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	github.com/streamingfast/logging v0.0.0-20250404134358-92b15d2fbd2e // indirect
	github.com/tidwall/hashmap v1.8.1 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/tyler-smith/go-bip39 v1.1.0 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/whyrusleeping/cbor-gen v0.0.0-20230818171029-f91ae536ca25 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/ybbus/jsonrpc/v3 v3.1.5 // indirect
	github.com/zeebo/xxh3 v1.0.2 // indirect
	go.mongodb.org/mongo-driver/v2 v2.5.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/ratelimit v0.3.1 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/arch v0.0.0-20210923205945-b76863e36670 // indirect
	golang.org/x/crypto v0.50.0 // indirect
	golang.org/x/exp v0.0.0-20250620022241-b7579e27df2b // indirect
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/term v0.42.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	golang.org/x/time v0.11.0 // indirect
	golang.org/x/xerrors v0.0.0-20231012003039-104605ab7028 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	k8s.io/klog/v2 v2.90.1 // indirect
	lukechampine.com/blake3 v1.2.1 // indirect
	modernc.org/libc v1.37.6 // indirect
	modernc.org/mathutil v1.6.0 // indirect
	modernc.org/memory v1.7.2 // indirect
	modernc.org/sqlite v1.28.0 // indirect
)
