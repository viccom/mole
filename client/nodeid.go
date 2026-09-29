package moleAgent_client

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shirou/gopsutil/v3/cpu"
)

const nodeIDLength = 8

// 虚拟网卡 name 前缀黑名单：这些网卡的 MAC 由虚拟化/容器运行时随机生成，
// 重启后会变化，不能用作机器唯一性指纹。注意 "br" 同时覆盖 "br0" 和 "br-xxx"。
var virtualNICPrefixes = []string{
	"br",      // Docker / Linux bridge (br0, br-xxx)
	"veth",    // Docker veth pair
	"docker",  // docker0 默认网桥
	"virbr",   // libvirt 网桥
	"tun",     // TUN 隧道
	"tap",     // TAP 设备
	"wg",      // WireGuard
	"ovs",     // Open vSwitch
	"flannel", // flannel CNI
	"cni",     // CNI 通用
	"calico",  // calico CNI
}

// 物理网卡 name 前缀（Linux 常见命名）：仅作 sysfs 不可用时的 fallback。
// 注意不含 "eth"：容器内 eth0 是 veth 对端（MAC 随机），无法与宿主机物理 eth0 靠前缀区分。
var physicalNICPrefixes = []string{
	"ens", "enp", "em", "eno", "enx", "wl",
}

// HardwareNodeID 基于 CPU + MAC + machine-id 生成确定性节点 ID（8 字符，首字符字母）。
// 内部读取一次 machine-id；需要与调用方共用同一次读取结果时用 hardwareNodeID。
func HardwareNodeID() (string, error) {
	return hardwareNodeID(readMachineID())
}

// hardwareNodeID 用给定的 machineID 计算硬件指纹。machineID 为空时只用 CPU + MAC。
func hardwareNodeID(machineID string) (string, error) {
	var parts []string

	info, err := cpu.Info()
	if err == nil && len(info) > 0 {
		parts = append(parts, info[0].VendorID, info[0].ModelName)
	}

	if mac := firstHardwareMAC(); mac != "" {
		parts = append(parts, mac)
	}

	if machineID != "" {
		parts = append(parts, machineID)
	}

	if len(parts) == 0 {
		return "", fmt.Errorf("no hardware info available")
	}

	hash := sha256.Sum256([]byte(strings.Join(parts, "|")))
	id := hex.EncodeToString(hash[:])[:8]
	if id[0] >= '0' && id[0] <= '9' {
		id = string(rune(id[0]-'0'+'a')) + id[1:]
	}
	return id, nil
}

// firstHardwareMAC 返回第一个非回环网卡的 MAC 地址（小写冒号分隔）。
// 优先物理网卡，跳过 Docker/虚拟化产生的随机 MAC 网卡。
func firstHardwareMAC() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	return selectHardwareMAC(interfaces)
}

// selectHardwareMAC 从给定网卡列表中选择最稳定的 MAC 地址。
// 三轮扫描，稳定性从高到低：
//  1. sysfs 判定为物理网卡的（/sys/class/net/<name>/device 软链接存在），MAC 最稳定
//  2. 名称匹配物理前缀的（sysfs 不可用时的 fallback）
//  3. 排除已知虚拟网卡后的其余网卡（纯虚拟环境兜底）
//
// 排序保证同环境下确定性。
func selectHardwareMAC(interfaces []net.Interface) string {
	sort.Slice(interfaces, func(i, j int) bool {
		return interfaces[i].Name < interfaces[j].Name
	})

	usable := func(iface net.Interface) bool {
		return iface.Flags&net.FlagLoopback == 0 &&
			iface.Flags&net.FlagUp != 0 &&
			len(iface.HardwareAddr) != 0
	}

	// 第一轮：sysfs 判定为物理网卡（PCI/USB 设备有 device 软链接，虚拟/lo 无）
	for _, iface := range interfaces {
		if !usable(iface) || !isPhysicalNICBySysfs(iface.Name) {
			continue
		}
		return iface.HardwareAddr.String()
	}

	// 第二轮：名称匹配物理前缀（sysfs 不可用时的 fallback）
	for _, iface := range interfaces {
		if !usable(iface) || !hasPrefix(iface.Name, physicalNICPrefixes) {
			continue
		}
		return iface.HardwareAddr.String()
	}

	// 第三轮：排除已知虚拟网卡，接受其余
	for _, iface := range interfaces {
		if !usable(iface) || hasPrefix(iface.Name, virtualNICPrefixes) {
			continue
		}
		return iface.HardwareAddr.String()
	}
	return ""
}

// isPhysicalNICBySysfs 通过 /sys/class/net/<name>/device 软链接是否存在判定物理网卡。
// 物理 PCI/USB 网卡有该软链接；虚拟网卡（veth/bridge/tun/lo）和容器内的 eth0（veth 对端）无。
// 非 Linux 或 sysfs 不可用时返回 false。包级变量便于测试注入。
var isPhysicalNICBySysfs = defaultIsPhysicalNICBySysfs

func defaultIsPhysicalNICBySysfs(name string) bool {
	if name == "" {
		return false
	}
	_, err := os.Stat(filepath.Join("/sys/class/net", name, "device"))
	return err == nil
}

// hasPrefix 判断 name 是否以 list 中任一前缀开头
func hasPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// readMachineID 读取 Linux machine-id
func readMachineID() string {
	for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if data, err := os.ReadFile(path); err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}

// nodeIDFile 存放 node.id 持久化文件的完整路径。包级变量便于测试注入与配置覆盖。
// 该文件是新模型下 nodeID 的唯一真相源，路径稳定性直接决定 nodeID 稳定性。
var nodeIDFile = defaultNodeIDFile()

// defaultNodeIDFile 返回 ~/.moleAgent-client/node.id；HOME 不可用时回退到可执行文件同目录。
// 回退意味着路径依赖进程启动方式（交互式 vs systemd 服务），会导致同一机器读到不同文件、
// 进而生成不同 nodeID，因此回退必须输出 WARNING 让运维可见。
func defaultNodeIDFile() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".moleAgent-client", "node.id")
	}

	dir := "."
	if exe, err := os.Executable(); err == nil {
		dir = filepath.Dir(exe)
	}
	log.Printf("WARNING: HOME unavailable, falling back to %q for node.id; "+
		"set node_id_file in the config to pin a stable path", filepath.Join(dir, "node.id"))
	return filepath.Join(dir, "node.id")
}

// SetNodeIDFile 覆盖 node.id 路径（来自配置项 node_id_file）。
// 必须在读取/写入 nodeID 之前调用（New 中先于 ApplyDefaults）。
func SetNodeIDFile(path string) {
	if path != "" {
		nodeIDFile = path
	}
}

// persistedID 持久化记录：machine-id 源值 + 首次生成的 nodeID
type persistedID struct {
	MachineID string `json:"machine_id"`
	NodeID    string `json:"node_id"`
}

// loadPersistedID 读取持久化记录。文件不存在或损坏时返回 ok=false（不报错）
func loadPersistedID() (machineID, nodeID string, ok bool) {
	data, err := os.ReadFile(nodeIDFile)
	if err != nil {
		return "", "", false
	}
	var p persistedID
	if err := json.Unmarshal(data, &p); err != nil {
		return "", "", false
	}
	if p.NodeID == "" || !ValidateNodeID(p.NodeID) {
		return "", "", false
	}
	return p.MachineID, p.NodeID, true
}

// savePersistedID 原子写入持久化记录。写失败仅 warning 不阻断。
// 采用「写临时文件 + os.Rename」保证原子性：进程在任何时刻崩溃，
// node.id 要么是完整的旧内容、要么是完整的新内容，不会出现截断的中间态。
func savePersistedID(machineID, nodeID string) {
	p := persistedID{MachineID: machineID, NodeID: nodeID}
	data, err := json.Marshal(p)
	if err != nil {
		log.Printf("WARNING: marshal node.id failed: %v", err)
		return
	}
	dir := filepath.Dir(nodeIDFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("WARNING: create node.id dir %q failed: %v", dir, err)
		return
	}
	if err := writeFileAtomic(nodeIDFile, data, 0o600); err != nil {
		log.Printf("WARNING: write node.id failed: %v", err)
	}
}

// writeFileAtomic 原子写文件：先写同目录临时文件并 fsync，再 rename 覆盖目标，最后 fsync 目录。
// 同目录 rename 在 POSIX 上是原子的，保证目标文件不会出现截断中间态。
// 包级变量便于测试注入故障。
var writeFileAtomic = defaultWriteFileAtomic

func defaultWriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".node.id.tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// 失败时清理临时文件
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	// 数据必须在 rename 前落盘：否则断电后 rename 已生效但数据块未回写，
	// 会留下零填充/截断的 node.id，下次启动被当作首次运行而重建 nodeID。
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	// 目录 fsync 使 rename 本身持久化，避免崩溃后丢失重命名。尽力而为：
	// 失败时文件已就位可正常读取，仅在断电场景下退化为「rename 未持久化」。
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// pickNodeID 选定 nodeID：硬件 ID 合法则用它，否则随机兜底
func pickNodeID(hwNodeID string, err error) string {
	if err == nil && hwNodeID != "" && ValidateNodeID(hwNodeID) {
		return hwNodeID
	}
	return GenerateNodeID()
}

// pseudoMachineIDPrefix 伪 machine-id 前缀，标识该值并非来自系统（容器/macOS/Windows）。
const pseudoMachineIDPrefix = "pseudo-"

// 可注入的探测函数（测试用，避免依赖真实环境）
var (
	readMachineIDFn  = readMachineID
	hardwareNodeIDFn = hardwareNodeID
)

// DefaultNodeID 生成默认节点ID，**只读不写**（验证路径如 validateDesktopNode 会调用它）。
func DefaultNodeID() string {
	return currentNodeID(false)
}

// EnsureNodeIDPersisted 解析并（必要时）落盘 nodeID，供真实启动路径调用。
func EnsureNodeIDPersisted() string {
	return currentNodeID(true)
}

// currentNodeID 解析当前 nodeID 的公共实现。
//
// 文件存在 → 以文件为准（锁定 node_id），只读 machine-id 做一致性提示，**无需硬件探测**；
// 文件不存在（首次/损坏）→ 用硬件指纹生成，machine-id 只读一次并传入指纹计算，
// 避免同一流程内重复读取导致的不一致。persist 为真时按 resolveNodeID 的决策落盘。
func currentNodeID(persist bool) string {
	persistedMachine, persistedNode, hadFile := loadPersistedID()
	if hadFile {
		// 文件为准：hwNodeID 不参与，跳过 CPU/网卡探测
		id, _, _ := resolveNodeID(readMachineIDFn(), "", nil, persistedMachine, persistedNode, true)
		return id
	}

	hwMachineID := readMachineIDFn()
	hwNodeID, hwErr := hardwareNodeIDFn(hwMachineID)
	id, persistMachineID, needPersist := resolveNodeID(hwMachineID, hwNodeID, hwErr, "", "", false)
	if persist && needPersist {
		savePersistedID(persistMachineID, id)
	}
	return id
}

// resolveNodeID 是 nodeID 解析的纯逻辑核心，便于单测。
// 返回 (最终 nodeID, 要持久化的 machine-id, 是否需要落盘)。
//
// 文件是 nodeID 的真相源：
//   - 文件存在 → 直接返回文件中的 node_id（锁定，不落盘）。系统 machine-id 与文件记录
//     不一致或系统无 machine-id 时，仅输出 warning 提示，不改变 nodeID。
//   - 文件不存在（首次/损坏）→ 硬件指纹生成 nodeID；machine-id 取系统值，
//     系统无 machine-id（容器/macOS/Windows）时生成伪 machine-id 一并落盘，
//     保证后续重启（MAC 变化）仍能锁定同一 nodeID。
func resolveNodeID(hwMachineID, hwNodeID string, hwErr error,
	persistedMachine, persistedNode string, hadFile bool) (string, string, bool) {
	if hadFile {
		// 文件为准：锁定 nodeID，machine-id 仅做一致性提示
		switch {
		case hwMachineID == "" && persistedMachine != "":
			log.Printf("WARNING: system machine-id unavailable; using persisted machine-id %q, node ID %q locked", persistedMachine, persistedNode)
		case hwMachineID != "" && persistedMachine != "" && hwMachineID != persistedMachine:
			log.Printf("WARNING: machine-id drifted (persisted=%q, current=%q); keeping persisted node ID %q", persistedMachine, hwMachineID, persistedNode)
		}
		return persistedNode, "", false
	}

	// 首次：硬件指纹生成 + 落盘
	id := pickNodeID(hwNodeID, hwErr)
	mid := hwMachineID
	if mid == "" {
		mid = generatePseudoMachineID()
		log.Printf("WARNING: no system machine-id available; generated pseudo machine-id %q for node %q", mid, id)
	}
	return id, mid, true
}

// generatePseudoMachineID 生成伪 machine-id（随机十六进制 + 前缀标识）。
// 仅在没有系统 machine-id 的机器（容器/macOS/Windows）首次启动时生成一次并落盘，
// 后续启动直接复用文件中的值，从而保证 nodeID 稳定。
func generatePseudoMachineID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("crypto/rand.Read failed: %v", err))
	}
	return pseudoMachineIDPrefix + hex.EncodeToString(buf)
}

// GenerateNodeID 生成随机 8 字符节点 ID（首字符字母，其余字母或数字）
// 作为硬件 ID 不可用时的 fallback
func GenerateNodeID() string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	const alphanum = "abcdefghijklmnopqrstuvwxyz0123456789"

	seed := make([]byte, nodeIDLength)
	if _, err := rand.Read(seed); err != nil {
		panic(fmt.Sprintf("crypto/rand.Read failed: %v", err))
	}

	id := make([]byte, nodeIDLength)
	id[0] = letters[seed[0]%byte(len(letters))]
	for i := 1; i < nodeIDLength; i++ {
		id[i] = alphanum[seed[i]%byte(len(alphanum))]
	}
	return string(id)
}

// ValidateNodeID 校验节点 ID 格式：固定 8 个 ASCII 字符，首字符字母，其余字母或数字。
// 逐字节校验（不用 unicode.IsLetter），避免多字节字符因数长度恰好为 8 字节而混过。
// 必须与服务端 tunnel.isValidNodeID 保持一致。
func ValidateNodeID(id string) bool {
	if len(id) != nodeIDLength {
		return false
	}
	for i := 0; i < nodeIDLength; i++ {
		c := id[i]
		isLetter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if i == 0 {
			if !isLetter {
				return false
			}
			continue
		}
		if !isLetter && !(c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
