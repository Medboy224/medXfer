// The pairing link and the access point it may carry (WP-B.7).
import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/connection/pair_link.dart';

const _core = 'medxfer://v1/pair?fp=BDIT4HBW&a=10.46.56.232&p=18887&s=abc&exp=1900000000';

void main() {
  test('a link of the core alone: no network', () {
    final link = PairLink.parse('  $_core \n')!;
    expect(link.uri, _core);
    expect(link.network, isNull);
  });

  test('the network goes in and comes out whole, whatever its name holds', () {
    const n = LinkNetwork(ssid: 'Café & co =1', passphrase: 'p@ss word&x', security: 2);
    final uri = PairLink.withNetwork(_core, n);
    expect(uri, startsWith('$_core&ws='));
    final back = PairLink.parse(uri)!;
    expect(back.network!.ssid, n.ssid);
    expect(back.network!.passphrase, n.passphrase);
    expect(back.network!.security, 2);
    // The core reads the same link: its own fields are untouched.
    expect(Uri.parse(back.uri).queryParameters['a'], '10.46.56.232');
  });

  test('anything else is no pairing link', () {
    expect(PairLink.parse('https://example.com/pair'), isNull);
    expect(PairLink.parse('medxfer://v1/other?fp=A'), isNull);
    expect(PairLink.parse('WIFI:S:home;T:WPA;P:x;;'), isNull);
    expect(PairLink.parse(''), isNull);
  });
}
