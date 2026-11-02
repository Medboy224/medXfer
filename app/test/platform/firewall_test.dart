import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/connection/firewall_watch.dart';
import 'package:medxfer/platform/firewall.dart';

/// Windows' firewall as the second PC of the real test of 1 November had it: medXfer let in on
/// its cable (« Public ») only, its Wi-Fi « Private ».
class _Firewall implements Firewall {
  bool privateAllowed = false;
  bool? answer = true;
  int asked = 0;

  @override
  Future<List<FirewallNetwork>> status() async => [
        const FirewallNetwork(name: 'Réseau non identifié', category: 'public', allowed: true),
        FirewallNetwork(name: 'TECNO POP 7', category: 'private', allowed: privateAllowed),
      ];

  @override
  Future<bool> allow() async {
    asked++;
    if (answer == true) privateAllowed = true;
    return answer ?? false;
  }
}

void main() {
  test('real test of 1 November: the network medXfer is kept out of is found, and let in once allowed', () async {
    final fw = _Firewall();
    final watch = FirewallWatch(fw);
    await watch.check();
    expect(watch.blocked.map((n) => (n.name, n.category)), [('TECNO POP 7', 'private')]);

    fw.answer = false; // refused at the system's prompt
    await watch.allow();
    expect((watch.lastAnswer, watch.blocked.length, watch.asking), (false, 1, false));

    fw.answer = true;
    await watch.allow();
    expect(watch.blocked, isEmpty);
    expect((watch.lastAnswer, fw.asked), (true, 2));
    watch.dispose();
  });

  test('nothing to watch where there is no firewall to read', () async {
    final watch = FirewallWatch(const NoFirewall());
    watch.start(); // no timer left running
    await watch.check();
    expect(watch.blocked, isEmpty);
    watch.dispose();
  });
}
