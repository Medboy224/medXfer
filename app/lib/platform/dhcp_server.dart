/// A DHCP server for the network a Windows computer creates (WP-C.2 of the J6c plan): Windows
/// serves no addresses on its Wi-Fi Direct group, so a device that joined it got none (probe of
/// 28 October: `ip-config-unavailable` after 45 s). The app serves them, the least RFC 2131
/// needs: DISCOVER gets an OFFER, REQUEST an ACK or a NAK. No router, no DNS: the network
/// carries medXfer, not the Internet.
///
/// Bound to the group's own address, never to every interface: it answers no request from
/// another network the computer is on. Its range is the group's subnet, link-local
/// (169.254.0.0/16) when Windows gave the group nothing else; no administrator rights.
library;

import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

/// Who got which address: one per device (its hardware address), within the server's subnet.
class DhcpLeases {
  DhcpLeases({required this.server, required this.prefix, this.size = 200});

  /// The server's address, the group's.
  final InternetAddress server;
  final int prefix;

  /// How many addresses, after the server's.
  final int size;

  final _byMac = <String, int>{};

  int get _serverInt => _toInt(server.rawAddress);
  int get _mask => prefix == 0 ? 0 : (0xFFFFFFFF << (32 - prefix)) & 0xFFFFFFFF;

  /// The subnet mask, for the clients.
  Uint8List get mask => _fromInt(_mask);

  /// The address [mac] has, or a free one; null once the range is full.
  int? leaseFor(String mac) {
    final had = _byMac[mac];
    if (had != null) return had;
    final taken = _byMac.values.toSet();
    for (var i = 1; i <= size; i++) {
      final a = (_serverInt + i) & 0xFFFFFFFF;
      final host = a & ~_mask & 0xFFFFFFFF;
      // Not the subnet's own address nor its broadcast, nor a .0 or .255 some stacks refuse.
      if ((a & _mask) != (_serverInt & _mask) || host == 0 || host == (~_mask & 0xFFFFFFFF)) continue;
      if ((a & 0xFF) == 0 || (a & 0xFF) == 0xFF || taken.contains(a)) continue;
      return _byMac[mac] = a;
    }
    return null;
  }

  /// [mac] asks for [address]: its own, or a free one of the range, is granted.
  bool grant(String mac, int address) {
    final had = _byMac[mac];
    if (had != null) return had == address;
    final offered = leaseFor(mac);
    return offered == address;
  }

  void release(String mac) => _byMac.remove(mac);
}

const _magic = [99, 130, 83, 99];
const _discover = 1, _offer = 2, _request = 3, _ack = 5, _nak = 6, _release = 7;

/// The answer to one DHCP request, or null: not a request for this server, or nothing to say.
Uint8List? answerDhcp(Uint8List q, DhcpLeases leases, {Duration lease = const Duration(hours: 1)}) {
  if (q.length < 240 || q[0] != 1 || q[1] != 1 || q[2] != 6) return null; // a BOOTREQUEST, Ethernet
  if (q[236] != _magic[0] || q[237] != _magic[1] || q[238] != _magic[2] || q[239] != _magic[3]) return null;
  final options = _options(q.sublist(240));
  final type = options[53]?.firstOrNull;
  final mac = [for (final b in q.sublist(28, 34)) b.toRadixString(16).padLeft(2, '0')].join(':');
  final server = _toInt(leases.server.rawAddress);
  final serverId = options[54];
  if (serverId != null && serverId.length == 4 && _toInt(serverId) != server) {
    if (type == _request) leases.release(mac); // it chose another server
    return null;
  }
  int reply;
  int? yiaddr;
  switch (type) {
    case _discover:
      yiaddr = leases.leaseFor(mac);
      if (yiaddr == null) return null;
      reply = _offer;
    case _request:
      final asked = options[50];
      final wanted = asked != null && asked.length == 4 ? _toInt(asked) : _toInt(q.sublist(12, 16));
      if (leases.grant(mac, wanted)) {
        yiaddr = wanted;
        reply = _ack;
      } else {
        reply = _nak;
      }
    case _release:
      leases.release(mac);
      return null;
    default:
      return null;
  }
  final out = BytesBuilder()
    ..add([2, 1, 6, 0]) // BOOTREPLY, Ethernet, 6-byte hardware address, no hops
    ..add(q.sublist(4, 8)) // xid
    ..add([0, 0]) // secs
    ..add([0x80, 0]) // flags: broadcast, the client has no address yet
    ..add([0, 0, 0, 0]) // ciaddr
    ..add(yiaddr == null ? [0, 0, 0, 0] : _fromInt(yiaddr))
    ..add(leases.server.rawAddress) // siaddr
    ..add([0, 0, 0, 0]) // giaddr
    ..add(q.sublist(28, 44)) // chaddr
    ..add(Uint8List(192)) // sname, file
    ..add(_magic)
    ..add([53, 1, reply])
    ..add([54, 4, ...leases.server.rawAddress]);
  if (reply != _nak) {
    out
      ..add([51, 4, ..._fromInt(lease.inSeconds)])
      ..add([1, 4, ...leases.mask]);
  }
  out.add([255]);
  final bytes = out.toBytes();
  return bytes.length >= 300 ? bytes : (Uint8List(300)..setRange(0, bytes.length, bytes)); // BOOTP's least
}

Map<int, Uint8List> _options(Uint8List o) {
  final out = <int, Uint8List>{};
  var i = 0;
  while (i < o.length) {
    final code = o[i];
    if (code == 255) break;
    if (code == 0) {
      i++;
      continue;
    }
    if (i + 1 >= o.length) break;
    final len = o[i + 1];
    if (i + 2 + len > o.length) break;
    out[code] = o.sublist(i + 2, i + 2 + len);
    i += 2 + len;
  }
  return out;
}

int _toInt(List<int> b) => (b[0] << 24) | (b[1] << 16) | (b[2] << 8) | b[3];
Uint8List _fromInt(int v) => Uint8List.fromList([(v >> 24) & 0xFF, (v >> 16) & 0xFF, (v >> 8) & 0xFF, v & 0xFF]);

/// The server on its socket: requests on the group's address, port 67; answers broadcast to
/// port 68, the client having no address yet.
class DhcpServer {
  DhcpServer._(this._socket, this.leases);

  final RawDatagramSocket _socket;
  final DhcpLeases leases;

  static Future<DhcpServer> start(InternetAddress server, int prefix) async {
    final socket = await RawDatagramSocket.bind(server, 67);
    socket.broadcastEnabled = true;
    final s = DhcpServer._(socket, DhcpLeases(server: server, prefix: prefix));
    socket.listen((event) {
      if (event != RawSocketEvent.read) return;
      final d = socket.receive();
      if (d == null) return;
      final reply = answerDhcp(d.data, s.leases);
      if (reply != null) socket.send(reply, InternetAddress('255.255.255.255'), 68);
    });
    return s;
  }

  void close() => _socket.close();
}
