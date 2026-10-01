// Package noisechan 实现 mole 控制通道的 Noise XXpsk2 传输加密升级（认证后、
// smux 前插入），服务端与客户端共用同一实现。
//
// 套件固定 Noise_XXpsk2_25519_ChaChaPoly_SHA256。flynn/noise v1.1.0 未预置任何
// PSK pattern（patterns.go 只有 15 个无 PSK 变体），但其 Config 支持任意 PSK 位次
// 声明：PresharedKeyPlacement=2 时 NewHandshakeState 把 MessagePatternPSK 追加到
// HandshakeXX 的 msg2 token 末尾（psk 位于 message 2 之后），协议名自动成为
// "Noise_XXpsk2_..."，与 Noise 规范的 XXpsk2 逐 token 一致；该机制经库自带官方
// 测试向量验证（vector_test.go 跑 vectors.txt 中的 XXpsk2 向量）。
//
// 分帧格式：2 字节大端长度前缀 + 密文体，握手 3 条消息与数据消息同格式。
package noisechan

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/flynn/noise"
)

// MaxMessageLen 单条分帧消息（密文体）上限：2 字节长度前缀可表达的最大值。
const MaxMessageLen = 65535

// tagLen AEAD 认证标签字节数（ChaCha20-Poly1305 为 16）。
const tagLen = 16

// maxPlaintextLen 单帧明文上限：必须为认证标签预留空间，否则密文长度会超出
// 2 字节前缀可表达范围（65535 明文 + 16 tag = 65551 > 65535）。
const maxPlaintextLen = MaxMessageLen - tagLen

// minFrameLen 合法帧体下界：密文帧至少含 16 字节认证标签（握手最小帧 msg1 =
// 32 字节临时公钥 + 16 字节 tag 共 48 字节，同样不低于此界；XXpsk2 的 e token
// 在 PSK 模式下也带 AEAD tag，官方向量 msg_0_ciphertext 即 48 字节）。低于此界
// 说明帧流已错位或对端损坏。
const minFrameLen = tagLen

// ErrHandshake 握手失败哨兵。错误文案以 "channel encryption handshake failed"
// 为前缀——客户端重连日志靠它识别「已宣告加密能力后的握手失败」，与旧服务端
// 缺 enc 字段的明文回落严格区分。
var ErrHandshake = errors.New("channel encryption handshake failed")

// suite 固定套件 25519_ChaChaPoly_SHA256；pattern 取 HandshakeXX + psk2 位次
// 注入（见包注释）。
var suite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)

// Conn 是 Noise 加密层上的 net.Conn：读 = 从穿针 reader 分帧读密文并 AEAD 解密；
// 写 = AEAD 加密分帧后直写底层 conn。Deadline/Close/Addr 全部委托底层 conn。
type Conn struct {
	conn net.Conn

	// reader 必须是认证阶段使用的同一个 bufio.Reader（bufio 穿针）：认证行
	// 读取时该 reader 可能已把后续密文字节吞入缓冲，握手与连接全生命周期的
	// 分帧读都必须经它，任何一次直读底层 conn 都会跳过缓冲字节导致帧流
	// 错位、必然性解密失败。
	reader io.Reader

	wmu  sync.Mutex // noise.CipherState 非并发安全：写方向独占锁
	enc  *noise.CipherState
	wbuf []byte // 2 + MaxMessageLen，写侧复用（wmu 保护）

	rmu     sync.Mutex // 读方向独占锁：dec + pending + reader
	dec     *noise.CipherState
	pending []byte // 已解密、尚未被调用方读走的余量（短读续供）
	rbuf    []byte // 密文帧体复用缓冲（rmu 保护；Decrypt 输出到新切片，无别名）
}

// UpgradeInitiator 客户端侧（发起方）升级：认证完成后、应答 ok 且带 enc 能力
// 宣告时调用。psk = sha256(token) 32 字节。reader 必须是认证阶段读应答行的同一
// 个 bufio.Reader（穿针，见 Conn.reader）。
//
// 安全边界：握手一旦启动，任何失败（psk 错配、消息畸形、超时、网络错误）都
// 关闭底层 conn 并返回包裹 ErrHandshake 的错误，绝不回落明文——此时对端已宣告
// 加密能力，失败即意味着攻击或链路损坏；若在此处回落，主动中间人只需破坏
// msg2 即可获得明文。调用方收到错误后无需再 Close（重复 Close 无害）。
func UpgradeInitiator(conn net.Conn, reader io.Reader, psk []byte, timeout time.Duration) (*Conn, error) {
	return upgrade(conn, reader, psk, timeout, true)
}

// UpgradeResponder 服务端侧（应答方）升级：ok 应答（带 enc 能力宣告）写出之后、
// smux 建立之前调用。psk 为 proof 认证匹配成功的 token 的 sha256；reader 必须
// 是认证阶段读认证行的同一个 bufio.Reader（穿针，见 Conn.reader）。失败语义
// 同 UpgradeInitiator：关闭 conn、绝不回落。
func UpgradeResponder(conn net.Conn, reader io.Reader, psk []byte, timeout time.Duration) (*Conn, error) {
	return upgrade(conn, reader, psk, timeout, false)
}

// newHandshakeState 构造 XXpsk2 握手状态。XX 含静态密钥交换 token，库要求
// 调用方提供静态密钥对：每连接新生成一次性密钥对——本协议的对端身份由 psk
// （msg2 混入）与前置 proof 认证承担，静态密钥仅贡献转录绑定与 DH 派生。
func newHandshakeState(psk []byte, initiator bool) (*noise.HandshakeState, error) {
	static, err := suite.GenerateKeypair(nil)
	if err != nil {
		return nil, err
	}
	return noise.NewHandshakeState(noise.Config{
		CipherSuite:           suite,
		Pattern:               noise.HandshakeXX,
		Initiator:             initiator,
		StaticKeypair:         static,
		PresharedKey:          psk,
		PresharedKeyPlacement: 2,
	})
}

func upgrade(conn net.Conn, reader io.Reader, psk []byte, timeout time.Duration, initiator bool) (*Conn, error) {
	// 失败即断开：不留半升级连接，调用方凭 ErrHandshake 识别并自行重连。
	// 双 %w：外层哨兵 ErrHandshake 与内层具体因（如 os.ErrDeadlineExceeded）
	// 均可被 errors.Is 识别（Go 1.20+ 多 %w）
	fail := func(err error) error {
		conn.Close()
		return fmt.Errorf("%w: %w", ErrHandshake, err)
	}
	if len(psk) != 32 {
		return nil, fail(fmt.Errorf("psk must be 32 bytes (sha256(token)), got %d", len(psk)))
	}
	// 非正超时直接拒绝：本包是双端共用的公共 API，静默接受会让 stalled peer
	// 下的握手无限期阻塞（占死调用方 goroutine / 服务端同步 worker）。
	// 两个现有调用方均传 10s，此处为防御边界。
	if timeout <= 0 {
		return nil, fail(fmt.Errorf("handshake timeout must be positive, got %v", timeout))
	}
	// 超时预算覆盖握手全程（读写双向），成功后清除（进入 smux 前必须无残留 deadline）
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, fail(fmt.Errorf("set handshake deadline: %v", err))
	}
	hs, err := newHandshakeState(psk, initiator)
	if err != nil {
		return nil, fail(err)
	}
	c := &Conn{
		conn:   conn,
		reader: reader,
		wbuf:   make([]byte, 2+MaxMessageLen),
		rbuf:   make([]byte, MaxMessageLen),
	}

	// XXpsk2 三条消息 payload 均为空（无附加载荷）。
	// 拆出的双向 CipherState 方向约定（flynn/noise）：第一个 = 发起方→应答方，
	// 第二个 = 应答方→发起方。
	var send, recv *noise.CipherState
	if initiator {
		// -> e
		msg, _, _, err := hs.WriteMessage(nil, nil)
		if err != nil {
			return nil, fail(err)
		}
		if err := c.writeFrame(msg); err != nil {
			return nil, fail(fmt.Errorf("send message 1: %v", err))
		}
		// <- e, ee, s, es, psk：psk 在 msg2 末 token 混入，错配在此暴露
		//（msg2 尾部 AEAD tag 解密失败）
		body, err := c.readFrame()
		if err != nil {
			return nil, fail(fmt.Errorf("read message 2: %v", err))
		}
		if _, _, _, err := hs.ReadMessage(nil, body); err != nil {
			return nil, fail(fmt.Errorf("process message 2 (psk mismatch or tampering): %v", err))
		}
		// -> s, se：完成握手并拆出双向密钥
		msg, cs0, cs1, err := hs.WriteMessage(nil, nil)
		if err != nil {
			return nil, fail(err)
		}
		if err := c.writeFrame(msg); err != nil {
			return nil, fail(fmt.Errorf("send message 3: %v", err))
		}
		send, recv = cs0, cs1
	} else {
		// <- e
		body, err := c.readFrame()
		if err != nil {
			return nil, fail(fmt.Errorf("read message 1: %v", err))
		}
		if _, _, _, err := hs.ReadMessage(nil, body); err != nil {
			return nil, fail(fmt.Errorf("process message 1: %v", err))
		}
		// <- e, ee, s, es, psk（应答方写出）
		msg, _, _, err := hs.WriteMessage(nil, nil)
		if err != nil {
			return nil, fail(err)
		}
		if err := c.writeFrame(msg); err != nil {
			return nil, fail(fmt.Errorf("send message 2: %v", err))
		}
		// -> s, se：psk 错配在 msg3 的静态密钥块解密失败处暴露
		body, err = c.readFrame()
		if err != nil {
			return nil, fail(fmt.Errorf("read message 3: %v", err))
		}
		_, cs0, cs1, err := hs.ReadMessage(nil, body)
		if err != nil {
			return nil, fail(fmt.Errorf("process message 3 (psk mismatch or tampering): %v", err))
		}
		send, recv = cs1, cs0
	}

	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, fail(fmt.Errorf("clear deadline after handshake: %v", err))
	}
	c.enc, c.dec = send, recv
	return c, nil
}

// Read 读一帧解密内容拷入 p；p 小于帧长时余量缓存在 pending 供后续 Read 续供
// （smux 用 8 字节小缓冲读帧头，不支持短读即丢数据）。帧流错位、截断、MAC 失败
// 都返回明确错误，绝不静默重试；对端干净关闭时返回 io.EOF。
func (c *Conn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for len(c.pending) == 0 {
		body, err := c.readFrame()
		if err != nil {
			return 0, err
		}
		pt, err := c.dec.Decrypt(nil, nil, body)
		if err != nil {
			return 0, fmt.Errorf("noisechan: frame decrypt failed (connection corrupted): %w", err)
		}
		c.pending = pt
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

// Write 加密分帧后直写底层 conn（不经过 reader）。len(p) > 单帧明文上限时拆多帧，
// 全部成功返回 len(p)。写侧互斥：CipherState.Encrypt 非并发安全。
func (c *Conn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	sent := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > maxPlaintextLen {
			chunk = chunk[:maxPlaintextLen]
		}
		// 直接在 wbuf[2:] 上加密（容量 2+MaxMessageLen 足够，不会重分配）
		ct, err := c.enc.Encrypt(c.wbuf[2:2], nil, chunk)
		if err != nil {
			return sent, fmt.Errorf("noisechan: encrypt: %w", err)
		}
		binary.BigEndian.PutUint16(c.wbuf[:2], uint16(len(ct)))
		if _, err := c.conn.Write(c.wbuf[:2+len(ct)]); err != nil {
			return sent, err
		}
		p = p[len(chunk):]
		sent += len(chunk)
	}
	return sent, nil
}

// readFrame 经穿针 reader 读一帧：2 字节大端长度 + 密文体。声明长度越界或读中
// 途 EOF/错误即返回明确错误（帧流错位/对端损坏）。调用方须持有 rmu 或处于握手
// 独占期。返回的切片别名 rbuf，仅在本锁区间内有效。
func (c *Conn) readFrame() ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(c.reader, hdr[:]); err != nil {
		if err == io.EOF {
			return nil, io.EOF // 干净关闭（帧边界上），透传标准 EOF
		}
		return nil, fmt.Errorf("noisechan: frame header read: %w", err)
	}
	n := int(binary.BigEndian.Uint16(hdr[:]))
	// 上界虽被 uint16 天然约束（恒 ≤ MaxMessageLen），仍显式校验以固化协议
	// 不变量（若上限下调，此处即保护点）
	if n < minFrameLen || n > MaxMessageLen {
		return nil, fmt.Errorf("noisechan: corrupted frame: declared body length %d outside [%d, %d]", n, minFrameLen, MaxMessageLen)
	}
	body := c.rbuf[:n]
	if _, err := io.ReadFull(c.reader, body); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, fmt.Errorf("noisechan: truncated frame body (declared %d bytes): %w", n, err)
	}
	return body, nil
}

// writeFrame 写一帧（握手冷路径专用；数据路径走 Write 的复用缓冲实现）。
func (c *Conn) writeFrame(body []byte) error {
	if len(body) == 0 || len(body) > MaxMessageLen {
		return fmt.Errorf("noisechan: invalid handshake frame length %d", len(body))
	}
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(body)))
	if _, err := c.conn.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := c.conn.Write(body); err != nil {
		return err
	}
	return nil
}

// 以下全部委托底层 conn。

func (c *Conn) Close() error                       { return c.conn.Close() }
func (c *Conn) LocalAddr() net.Addr                { return c.conn.LocalAddr() }
func (c *Conn) RemoteAddr() net.Addr               { return c.conn.RemoteAddr() }
func (c *Conn) SetDeadline(t time.Time) error      { return c.conn.SetDeadline(t) }
func (c *Conn) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *Conn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }
