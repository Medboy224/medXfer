import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/platform/hotspot.dart';

void main() {
  test('HotspotInfo reads what LocalHotspot.kt sends', () {
    final info = HotspotInfo.fromMap({
      'ssid': 'AndroidShare_1732',
      'passphrase': 'secret',
      'security': 2,
      'address': '10.46.56.232',
      'interfaces': ['wlan0=192.168.1.20', 'swlan0=10.46.56.232'],
    });
    expect(info.ssid, 'AndroidShare_1732');
    expect(info.passphrase, 'secret');
    expect(info.security, 2);
    expect(info.address, '10.46.56.232');
    expect(info.interfaces, ['wlan0=192.168.1.20', 'swlan0=10.46.56.232']);
  });

  test('HotspotInfo of a join: an address, no network secrets', () {
    final info = HotspotInfo.fromMap({'address': '10.46.56.91', 'interfaces': null});
    expect(info.ssid, isNull);
    expect(info.passphrase, isNull);
    expect(info.security, 0);
    expect(info.interfaces, isEmpty);
  });
}
