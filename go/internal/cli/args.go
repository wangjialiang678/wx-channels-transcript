package cli

import "strings"

// Flags 是手写的命令行解析结果。
//
// 为什么不直接用标准库 flag：本工具允许选项出现在位置参数之后
// （`sph <链接> -o ./out`），并且同时接受 `-o` 与 `--out` 两种写法，
// flag 包在遇到第一个非选项参数时就会停止解析，做不到这一点。
type Flags struct {
	Values     map[string]string
	Bools      map[string]bool
	Positional []string
}

// valueFlags 是「后面必须跟一个值」的选项名（不含前导横线）。
var valueFlags = map[string]bool{
	"out": true, "o": true,
	"to": true, "t": true,
	"model": true, "m": true,
	"cookie": true, "browser": true,
	"lang": true, "l": true,
	"audio-format":  true,
	"hotwords":      true,
	"vocabulary-id": true,
}

// ParseArgs 解析命令行。语义与 Node 版保持一致。
func ParseArgs(argv []string) *Flags {
	f := &Flags{Values: map[string]string{}, Bools: map[string]bool{}}
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		name := strings.TrimLeft(a, "-")
		switch {
		case strings.HasPrefix(a, "-") && valueFlags[name]:
			if i+1 < len(argv) {
				f.Values[name] = argv[i+1]
				i++
			} else {
				f.Values[name] = ""
			}
		case strings.HasPrefix(a, "--"):
			f.Bools[a[2:]] = true
		case a == "-h":
			f.Bools["help"] = true
		case a == "-v":
			f.Bools["version"] = true
		default:
			f.Positional = append(f.Positional, a)
		}
	}
	return f
}

// Value 按顺序返回第一个被显式给出的选项值（如 out / o）。
func (f *Flags) Value(names ...string) string {
	for _, n := range names {
		if v, ok := f.Values[n]; ok {
			return v
		}
	}
	return ""
}

// Bool 报告某个布尔开关是否打开。
func (f *Flags) Bool(name string) bool { return f.Bools[name] }

// Any 报告是否给出了任何参数（位置或选项）。
func (f *Flags) Any() bool {
	return len(f.Positional) > 0 || len(f.Values) > 0 || len(f.Bools) > 0
}
