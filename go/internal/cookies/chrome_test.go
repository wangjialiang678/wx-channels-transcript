package cookies

import (
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestHostMatches(t *testing.T) {
	cases := []struct {
		hostKey, requestHost string
		want                 bool
	}{
		{"yuanbao.tencent.com", "yuanbao.tencent.com", true},
		{"yuanbao.tencent.com", "other.tencent.com", false},
		// hy_token / hy_user 实测挂在父域 .tencent.com 下，必须能匹配到
		{".tencent.com", "yuanbao.tencent.com", true},
		{".tencent.com", "tencent.com", true},
		{".tencent.com", "nottencent.com", false},
		{".tencent.com", "evil-tencent.com", false},
		{".qq.com", "yuanbao.tencent.com", false},
	}
	for _, c := range cases {
		if got := HostMatches(c.hostKey, c.requestHost); got != c.want {
			t.Errorf("HostMatches(%q, %q) = %v, want %v", c.hostKey, c.requestHost, got, c.want)
		}
	}
}

// encryptV10 按 Chrome 的方案加密一条 cookie 值，用于构造测试数据。
// 这正是 decryptValue 需要能解开的形态：v10 + AES-128-CBC + 16 个空格做 IV。
func encryptV10(t *testing.T, plain []byte, key, iv []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append([]byte{}, plain...)
	for i := 0; i < pad; i++ {
		padded = append(padded, byte(pad))
	}
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return append([]byte("v10"), out...)
}

func TestReadCookiesTable(t *testing.T) {
	key := []byte("0123456789abcdef") // 16 字节密钥
	iv := bytesRepeat(0x20, aes.BlockSize)

	dbPath := filepath.Join(t.TempDir(), "Cookies")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE cookies (
		host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB)`); err != nil {
		t.Fatal(err)
	}

	// 1) 域级 cookie（挂在父域 .tencent.com 下）——必须被匹配到
	// 2) host-only cookie——精确匹配才行
	// 3) 无关域——必须被排除
	// 4) 值里带控制字符的二进制 cookie——不能丢掉，但要洗掉控制字符
	rows := []struct {
		host, name, value string
		enc               []byte
	}{
		{".tencent.com", "hy_token", "", encryptV10(t, []byte("TOKEN123"), key, iv)},
		{"yuanbao.tencent.com", "plain", "PLAINVAL", nil},
		{"example.com", "other", "", encryptV10(t, []byte("NOPE"), key, iv)},
		{".tencent.com", "binaryish", "", encryptV10(t, []byte("a\x01b\x7fc"), key, iv)},
		// 新版 Chrome 会在明文前塞 32 字节的 domain hash，不可打印 → 应被剥掉
		{".tencent.com", "hashed", "", encryptV10(t, append(bytesRepeat(0x01, 32), []byte("REAL")...), key, iv)},
	}
	for _, r := range rows {
		if _, err := db.Exec("INSERT INTO cookies VALUES (?,?,?,?)", r.host, r.name, r.value, r.enc); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	jar, err := readCookiesTable(dbPath, "yuanbao.tencent.com", key, iv)
	if err != nil {
		t.Fatal(err)
	}

	if jar["hy_token"] != "TOKEN123" {
		t.Errorf("域级 cookie 未解出：%q", jar["hy_token"])
	}
	if jar["plain"] != "PLAINVAL" {
		t.Errorf("明文 cookie 未取到：%q", jar["plain"])
	}
	if _, ok := jar["other"]; ok {
		t.Error("不该把 example.com 的 cookie 混进来")
	}
	if v := jar["binaryish"]; v != "abc" {
		t.Errorf("二进制 cookie 应保留可打印字节并洗掉控制字符，得到 %q", v)
	}
	if v := jar["hashed"]; v != "REAL" {
		t.Errorf("应剥掉前置 32 字节 domain hash，得到 %q", v)
	}
}

func TestDecryptValueRejectsUnknownPrefix(t *testing.T) {
	key := []byte("0123456789abcdef")
	iv := bytesRepeat(0x20, aes.BlockSize)
	// v20 是 App-Bound 加密，明确不支持——应安静地返回空串
	if got := decryptValue(append([]byte("v20"), make([]byte, 32)...), key, iv); got != "" {
		t.Errorf("v20 应返回空串，得到 %q", got)
	}
	if got := decryptValue([]byte("ab"), key, iv); got != "" {
		t.Errorf("过短的输入应返回空串，得到 %q", got)
	}
}

func TestJarHeaderDeterministic(t *testing.T) {
	j := Jar{"b": "2", "a": "1"}
	if got := j.Header(); got != "a=1; b=2" {
		t.Errorf("Header() = %q", got)
	}
	if got := ParseHeader("a=1; b=2"); got["a"] != "1" || got["b"] != "2" {
		t.Errorf("ParseHeader 结果不对：%v", got)
	}
}
