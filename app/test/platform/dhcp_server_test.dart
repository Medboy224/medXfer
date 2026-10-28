// The DHCP server of a Windows computer's own network (WP-C.2 of the J6c plan): Windows serves
// no addresses on its Wi-Fi Direct group, the app does.
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/platform/dhcp_server.dart';

const _mac = [0x3e, 0x45, 0x31, 0xfe, 0x38, 0x91];
final _server = InternetAddress('169.254.121.148');

/// A client's request: DISCOVER (1) or REQUEST (3), with options.
Uint8List _request(int type, {List<int> mac = _mac, List<int>? asked, List<int>? serverId, List<int> xid = const [1, 2, 3, 4]}) {
  final b = BytesBuilder()
    ..add([1, 1, 6, 0])
    ..add(xid)
    ..add(Uint8List(20)) // secs, flags, ciaddr, yiaddr, siaddr, giaddr
    ..add([...mac, ...Uint8List(10)])
    ..add(Uint8List(192))
    ..add([99, 130, 83, 99])
    ..add([53, 1, type]);
  if (asked != null) b.add([50, 4, ...asked]);
  if (serverId != null) b.add([54, 4, ...serverId]);
  b.add([255]);
  return b.toBytes();
}

/// An answer's type (option 53) and offered address.
(int, String) _read(Uint8List a) {
  var i = 240;
  var type = 0;
  while (a[i] != 255) {
    if (a[i] == 53) type = a[i + 2];
    i += 2 + a[i + 1];
  }
  return (type, a.sublist(16, 20).join('.'));
}

/// The value of one option of an answer, or null.
List<int>? _option(Uint8List a, int code) {
  var i = 240;
  while (a[i] != 255) {
    if (a[i] == code) return a.sublist(i + 2, i + 2 + a[i + 1]);
    i += 2 + a[i + 1];
  }
  return null;
}

void main() {
  test('DISCOVER gets an OFFER in the group subnet, REQUEST its ACK: the same address each time', () {
    final leases = DhcpLeases(server: _server, prefix: 16);
    final offer = answerDhcp(_request(1), leases)!;
    final (type, address) = _read(offer);
    expect(type, 2);
    expect(address, '169.254.121.149');
    expect(offer.sublist(4, 8), [1, 2, 3, 4], reason: 'the same transaction');
    expect(offer[10] & 0x80, 0x80, reason: 'broadcast: the client has no address yet');
    expect(offer.length, greaterThanOrEqualTo(300));

    final ack = answerDhcp(_request(3, asked: [169, 254, 121, 149], serverId: [169, 254, 121, 148]), leases)!;
    expect(_read(ack), (5, '169.254.121.149'));
    // The mask is the group's: /16 (option 1).
    expect(_option(ack, 1), [255, 255, 0, 0]);
    expect(_option(ack, 3), isNull, reason: 'no router: the network carries medXfer, not the Internet');
    // Asked again (a renewal), the same.
    expect(_read(answerDhcp(_request(1), leases)!).$2, '169.254.121.149');
  });

  test('two devices, two addresses; a request for another one is refused (NAK)', () {
    final leases = DhcpLeases(server: _server, prefix: 16);
    answerDhcp(_request(1), leases);
    final other = [0x08, 0x11, 0x96, 0xde, 0x7a, 0x54];
    expect(_read(answerDhcp(_request(1, mac: other), leases)!).$2, '169.254.121.150');
    final nak = answerDhcp(_request(3, mac: other, asked: [169, 254, 121, 149]), leases)!;
    expect(_read(nak).$1, 6);
  });

  test('a request for another server, or not a DHCP request, gets nothing', () {
    final leases = DhcpLeases(server: _server, prefix: 16);
    expect(answerDhcp(_request(3, asked: [192, 168, 1, 20], serverId: [192, 168, 1, 1]), leases), isNull);
    expect(answerDhcp(Uint8List(300), leases), isNull);
    expect(answerDhcp(Uint8List.fromList([1, 1, 6]), leases), isNull);
  });

  test('a small subnet skips the server, the subnet and broadcast addresses, then is full', () {
    // 192.168.49.252/30: .252 the subnet, .253 the server, .254 the one host left, .255 the broadcast.
    final leases = DhcpLeases(server: InternetAddress('192.168.49.253'), prefix: 30, size: 10);
    expect(leases.leaseFor('a'), (192 << 24) | (168 << 16) | (49 << 8) | 254);
    expect(leases.leaseFor('b'), isNull, reason: 'full');
    final wide = DhcpLeases(server: InternetAddress('10.42.0.254'), prefix: 24, size: 3);
    expect(wide.leaseFor('a'), isNull, reason: '.255 is the broadcast, the range ends there');
  });
}
