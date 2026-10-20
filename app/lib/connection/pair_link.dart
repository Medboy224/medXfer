/// The pairing link of a QR code, and the network it may carry (WP-B.7, DISC-12): when one
/// device created its own access point, the QR code gives the other everything at once, the
/// network to join and the pairing. The core reads only its own fields (fp, a, p, s, exp) and
/// ignores the others; ws, wp and wt are the application's.
library;

/// The access point a link names: its name, passphrase, and security (SoftApConfiguration:
/// 1 WPA2, 2 WPA3 transition, 3 WPA3).
class LinkNetwork {
  const LinkNetwork({required this.ssid, required this.passphrase, this.security = 1});

  final String ssid;

  /// A secret of the access point: shown on the QR code's screen, never logged.
  final String passphrase;
  final int security;
}

class PairLink {
  const PairLink({required this.uri, this.network});

  /// Reads a scanned or pasted text; null when it is no medXfer pairing link.
  static PairLink? parse(String text) {
    final t = text.trim();
    final u = Uri.tryParse(t);
    if (u == null || u.scheme != 'medxfer' || u.host != 'v1' || u.path != '/pair') return null;
    final q = u.queryParameters;
    final ssid = q['ws'] ?? '';
    return PairLink(
      uri: t,
      network: ssid.isEmpty ? null : LinkNetwork(ssid: ssid, passphrase: q['wp'] ?? '', security: int.tryParse(q['wt'] ?? '') ?? 1),
    );
  }

  /// The core's link with network added: what the QR code of an access point shows.
  static String withNetwork(String uri, LinkNetwork n) {
    String e(String v) => Uri.encodeQueryComponent(v);
    return '$uri&ws=${e(n.ssid)}&wp=${e(n.passphrase)}&wt=${n.security}';
  }

  final String uri;
  final LinkNetwork? network;
}
