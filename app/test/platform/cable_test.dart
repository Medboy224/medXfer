import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/connection/cable_watch.dart';
import 'package:medxfer/platform/cable.dart';

/// nmcli as Zorin answers it (probe WP-E.0 of the cable plan), one wired card, recording each
/// call. The card's state is NetworkManager's: 20 no link, 30 disconnected, 70 waiting for an
/// address (DHCP), 100 connected, 120 failed.
class _Nmcli {
  final calls = <String>[];
  int state = 70;
  String connection = 'Connexion filaire 1';
  bool ipv4 = false;
  bool upFails = false;
  List<String> profiles = ['Connexion filaire 1', 'TECNO POP 7'];

  Future<ProcessResult> call(String exe, List<String> args) async {
    final line = '$exe ${args.join(' ')}';
    calls.add(line);
    var out = '';
    var code = 0;
    if (line.contains('device show')) {
      out =
          'GENERAL.DEVICE:wlo1\nGENERAL.TYPE:wifi\nGENERAL.STATE:100 (connected)\nGENERAL.CONNECTION:TECNO POP 7\n'
          'IP4.ADDRESS[1]:192.168.42.91/24\n\n'
          'GENERAL.DEVICE:enp0s25\nGENERAL.TYPE:ethernet\nGENERAL.STATE:$state (whatever)\nGENERAL.CONNECTION:$connection\n'
          '${ipv4 ? 'IP4.ADDRESS[1]:169.254.57.98/16\n' : ''}\n'
          'GENERAL.DEVICE:lo\nGENERAL.TYPE:loopback\nGENERAL.STATE:10 (unmanaged)\nGENERAL.CONNECTION:\nIP4.ADDRESS[1]:127.0.0.1/8\n';
    } else if (line == 'nmcli -t -f NAME connection show') {
      out = '${profiles.join('\n')}\n';
    } else if (line.contains('connection up ${LinuxCableLink.prefix}enp0s25')) {
      if (upFails) {
        code = 4;
      } else {
        state = 100;
        connection = '${LinuxCableLink.prefix}enp0s25';
        ipv4 = true;
      }
    }
    return ProcessResult(0, code, out, '');
  }

  Iterable<String> get gives => calls.where((c) => c.contains('connection add'));
}

class _FakeLink implements CableLink {
  Set<String> next = {};
  int checks = 0;
  bool released = false;

  @override
  Future<Set<String>> check() async {
    checks++;
    return next;
  }

  @override
  Future<void> release() async => released = true;

  @override
  Future<String?> sharingLeft() async => null;
}

void main() {
  late _Nmcli nm;
  late DateTime t;
  late LinuxCableLink link;
  setUp(() {
    nm = _Nmcli();
    t = DateTime(2026, 11, 1, 10);
    link = LinuxCableLink(run: nm.call, now: () => t);
  });

  test('WP-E.2: an address of the system\'s, or the user\'s own link-local profile, is left alone', () async {
    nm
      ..state = 100
      ..ipv4 = true;
    expect(await link.check(), isEmpty);
    t = t.add(const Duration(minutes: 5));
    expect(await link.check(), isEmpty);
    expect(nm.gives, isEmpty);
  });

  test('WP-E.2: a DHCP that is only slow is waited for; 50 s without IPv4, the app gives one', () async {
    nm.state = 70; // the default profile, asking DHCP
    expect(await link.check(), isEmpty);
    t = t.add(const Duration(seconds: 30));
    expect(await link.check(), isEmpty);
    expect(nm.gives, isEmpty, reason: 'a real network answers within seconds; nothing before NetworkManager gives up');
    t = t.add(const Duration(seconds: 21));
    expect(await link.check(), {'enp0s25'});
    expect(
      nm.gives.single,
      'nmcli connection add type ethernet ifname enp0s25 con-name medxfer-cable-enp0s25 '
      'ipv4.method link-local ipv6.method link-local connection.autoconnect no',
    );
    expect(nm.calls, contains('nmcli connection up medxfer-cable-enp0s25'));
    expect(
      nm.calls.where((c) => c.contains('Connexion filaire 1') && !c.contains('show')),
      isEmpty,
      reason: "the user's profile is never changed",
    );
  });

  test('WP-E.2: NetworkManager gave up its DHCP (probe: 46 s): the address at once', () async {
    nm.state = 70;
    await link.check();
    t = t.add(const Duration(seconds: 46));
    nm
      ..state = 30
      ..connection = '';
    expect(await link.check(), {'enp0s25'});
  });

  test('WP-E.2: a cable just plugged is not taken from NetworkManager before it tries', () async {
    nm
      ..state = 30
      ..connection = ''; // between "link connected" and the start of the activation
    expect(await link.check(), isEmpty);
    expect(nm.gives, isEmpty);
  });

  test("WP-E.2: a profile the user connected without IPv4 (IPv6 alone) is theirs", () async {
    nm
      ..state = 100
      ..ipv4 = false;
    await link.check();
    t = t.add(const Duration(minutes: 2));
    expect(await link.check(), isEmpty);
    expect(nm.gives, isEmpty);
  });

  test('WP-E.2: the cable unplugged, then the app closing: the profile goes each time', () async {
    nm.state = 70;
    await link.check();
    t = t.add(LinuxCableLink.dhcpWait);
    expect(await link.check(), {'enp0s25'});
    nm
      ..state = 20
      ..connection = ''
      ..ipv4 = false; // unplugged
    expect(await link.check(), isEmpty);
    expect(nm.calls.last, 'nmcli connection delete medxfer-cable-enp0s25');

    nm.state = 70; // plugged again: NetworkManager's own profile first, the wait again
    expect(await link.check(), isEmpty);
    t = t.add(LinuxCableLink.dhcpWait);
    expect(await link.check(), {'enp0s25'});
    nm.calls.clear();
    await link.release();
    expect(nm.calls, ['nmcli connection delete medxfer-cable-enp0s25']);
  });

  test('WP-E.2: a profile left by an app that did not close properly is deleted at the start', () async {
    nm
      ..profiles = ['Connexion filaire 1', 'medxfer-cable-enp0s25']
      ..state = 100
      ..ipv4 = true;
    await link.check();
    expect(nm.calls, contains('nmcli connection delete medxfer-cable-enp0s25'));
  });

  test('WP-E.2: a profile that does not come up is deleted, and tried again a minute later', () async {
    nm
      ..state = 70
      ..upFails = true;
    await link.check();
    t = t.add(LinuxCableLink.dhcpWait);
    expect(await link.check(), isEmpty);
    expect(nm.gives, hasLength(1));
    expect(nm.calls.last, 'nmcli connection delete medxfer-cable-enp0s25');
    t = t.add(const Duration(seconds: 30));
    await link.check();
    expect(nm.gives, hasLength(1), reason: 'not every 5 s');
    nm.upFails = false;
    t = t.add(const Duration(seconds: 31));
    expect(await link.check(), {'enp0s25'});
    expect(nm.gives, hasLength(2));
  });

  test('WP-E.2: the watch tells the cards given, and takes them back at the stop', () async {
    final fake = _FakeLink()..next = {'enp0s25'};
    final watch = CableWatch(fake, period: const Duration(hours: 1));
    var changes = 0;
    watch.addListener(() => changes++);
    watch.start();
    await pumpEventQueue();
    expect(watch.given, {'enp0s25'});
    expect((changes, fake.checks), (1, 1));
    await watch.stop();
    expect(watch.given, isEmpty);
    expect((fake.released, changes), (true, 2));
    watch.dispose();
  });

  test('WP-E.2: nothing to watch where the system gives the cable its address (Windows, phones)', () async {
    final watch = CableWatch(const NoCableLink());
    watch.start(); // no timer left running
    await watch.stop();
    expect(watch.given, isEmpty);
    watch.dispose();
  });

  test('WP-E.3: Windows says the sharing address left on a wired card, never on its own network', () async {
    var cards = <(String, List<String>)>[
      ('Wi-Fi', ['192.168.42.174']),
      ('Connexion au réseau local* 2', ['192.168.137.1']), // the computer's own network (WP-C.2)
      ('Ethernet', ['169.254.185.66']),
    ];
    final link = WindowsCableLink(interfaces: () async => cards);
    expect(await link.sharingLeft(), isNull);
    cards = [('Wi-Fi', ['192.168.42.174']), ('Ethernet', ['192.168.137.1'])];
    expect(await link.sharingLeft(), 'Ethernet');
    expect(await link.check(), isEmpty, reason: 'Windows gives the cable its address itself');
  });
}
