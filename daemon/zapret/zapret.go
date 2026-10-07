// Package zapret embeds the DPI-desync engine used to reach Discord and
// YouTube directly — outside the VPN tunnel — on networks that block them by
// inspecting the TLS/QUIC handshake (SNI) rather than the IP address itself.
//
// The technique: split the TLS ClientHello (where the blocked hostname sits)
// across two TCP segments at a byte offset chosen so a censor's DPI, which
// reads only the first segment, never sees the full hostname, while the real
// server — which reassembles the TCP stream properly — gets it intact. QUIC
// (YouTube over HTTP/3) and Discord's UDP voice/video get the same treatment
// with "fake" decoy packets instead, since there is no stream to split.
//
// The binaries are bol-van/zapret's own official release build (MIT),
// vendored unmodified — see assets/NOTICE.txt for the exact version, the
// sha256 of each file, and the license text. Nothing here is borrowed from a
// third-party wrapper: the hostlist and the command line are built by this
// application, not copied from one.
//
// This only ever applies to traffic sing-box's hybrid route sends to its
// "direct" outbound — a tunnelled connection has no plaintext ClientHello
// left for WinDivert to see by the time it reaches the physical adapter, so
// running this engine against tunnelled traffic would do nothing useful.
package zapret

import _ "embed"

//go:embed assets/winws.exe
var WinwsEXE []byte

//go:embed assets/WinDivert.dll
var WinDivertDLL []byte

//go:embed assets/WinDivert64.sys
var WinDivertSYS []byte

// TLSPattern is a real, captured TLS ClientHello for a well-known host. It is
// never sent anywhere — winws uses its bytes only as filler for the
// "sequence overlap" trick (--dpi-desync-split-seqovl-pattern), which needs
// plausible-looking TLS bytes in the overlapping region, not meaningful ones.
//
//go:embed assets/tls_clienthello_www_google_com.bin
var TLSPattern []byte

// QUICPattern is the equivalent sample for QUIC's encrypted Initial packet,
// used the same way for the --dpi-desync-fake-quic decoy.
//
//go:embed assets/quic_initial_www_google_com.bin
var QUICPattern []byte

// DiscordFake is zapret's own prebuilt decoy for Discord's UDP voice/video
// path (IP-discovery packet shape) — used as the fake packet winws sends
// ahead of real voice traffic on Discord's UDP port ranges.
//
//go:embed assets/discord-ip-discovery-with-port.bin
var DiscordFake []byte

// Domains are the hosts this engine is allowed to touch. The same list feeds
// two independent places that must never drift apart: sing-box's hybrid
// route rule (which hands this traffic to "direct" instead of the tunnel)
// and winws's own --hostlist (which limits WinDivert's desync to exactly
// these SNIs, so it never touches anything else, VPN handshake included).
//
// Deliberately narrower than a general-purpose anti-censorship hostlist:
// this exists for Discord and YouTube specifically, per the feature this
// shipped for, not as a general DPI-bypass tool. Add here, not in the UI,
// if the scope ever grows.
var Domains = []string{
	// Discord
	"discord.com",
	"discord.gg",
	"discordapp.com",
	"discordapp.net",
	"discord.media",
	"discordcdn.com",
	"dis.gd",

	// YouTube / the CDN hosts that actually carry video and playback API
	// traffic — not the whole of google.com or googleapis.com, which would
	// pull in a huge amount of unrelated Google traffic as "direct".
	"youtube.com",
	"youtube-nocookie.com",
	"youtu.be",
	"ytimg.com",
	"ggpht.com",
	"googlevideo.com",
	"youtubei.googleapis.com",
	"youtube.googleapis.com",
}

// DiscordUDPPortRanges are Discord's voice/video UDP ports, exactly as
// zapret's own filter examples document them.
const DiscordUDPPortRanges = "19294-19344,50000-50100"
