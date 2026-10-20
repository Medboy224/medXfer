// The foreground service held while something runs, released after (API-08, WP-B.4); and the
// network given again to the core when it changes (DISC-10).
import 'dart:async';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/connection/connection.dart';
import 'package:medxfer/core/core.dart';
import 'package:medxfer/core/fake_core.dart';
import 'package:medxfer/background_keeper.dart';
import 'package:medxfer/l10n/app_localizations.dart';
import 'package:medxfer/platform/background.dart';
import 'package:medxfer/platform/network.dart';
import 'package:medxfer/transfer/history.dart';
import 'package:medxfer/transfer/transfers.dart';

class _Background implements Background {
  final log = <String>[];

  @override
  bool get supported => true;

  @override
  Future<void> hold({required String title, required String text, int? progress}) async =>
      log.add('hold $title | $text${progress == null ? '' : ' | $progress'}');

  @override
  Future<void> release() async => log.add('release');

  @override
  Future<bool> askNotifications() async {
    log.add('ask');
    return true;
  }
}

class _Network implements NetworkInfo {
  final changed = StreamController<void>.broadcast();

  @override
  Future<List<NetInterface>?> interfaces() async => const [
    NetInterface(name: 'wlan0', addrs: ['192.168.1.20/24']),
  ];

  @override
  Future<void> multicast(bool on) async {}

  @override
  Stream<void> get changes => changed.stream;
}

void main() {
  final fr = lookupAppLocalizations(const Locale('fr'));

  testWidgets('a session, then a transfer, then nothing: held, updated, released', (tester) async {
    final core = FakeCore();
    final connection = Connection(core);
    final transfers = Transfers(core, history: MemoryHistoryStore(), peerName: () => 'PC', downloadDir: () async => '/x');
    final bg = _Background();
    final keep = BackgroundKeeper(background: bg, connection: connection, transfers: transfers, strings: () => fr);

    connection.take(const CoreEvent('paired', data: {'ip': '192.168.1.30', 'device_name': 'PC'}));
    expect(bg.log, ['ask', 'hold Connecté à PC | Ouvrez medXfer pour envoyer ou recevoir.']);

    transfers.take(const CoreEvent('file_offered', data: {'file_name': 'film.mkv', 'file_size': 100}));
    transfers.take(const CoreEvent('transfer_accepted', data: {}));
    transfers.take(const CoreEvent('transfer_progress', data: {'batch_bytes': 40, 'batch_total_bytes': 100, 'speed_mbps': 12.5}));
    // At most once a second: the progress waits for its turn.
    expect(bg.log.length, 2);
    await tester.pump(BackgroundKeeper.minInterval);
    expect(bg.log.last, 'hold Envoi à PC | 40 % · 12.5 Mo/s | 40');

    transfers.take(const CoreEvent('transfer_complete', data: {}));
    connection.take(const CoreEvent('disconnected', data: {'reason': 'peer requested disconnect'}));
    await tester.pump(BackgroundKeeper.minInterval);
    expect(bg.log.last, 'release');
    expect(bg.log.where((l) => l == 'ask'), hasLength(1), reason: 'the notifications are asked once');
    keep.dispose();
  });

  testWidgets('an offer waiting, the hotspot, the Connect screen each hold it', (tester) async {
    final core = FakeCore();
    final connection = Connection(core);
    final transfers = Transfers(core, history: MemoryHistoryStore(), peerName: () => '', downloadDir: () async => '/x');
    final keep = BackgroundKeeper(background: _Background(), connection: connection, transfers: transfers, strings: () => fr);
    expect(keep.wanted(), isNull);
    transfers.take(const CoreEvent('incoming_offer', data: {'device_name': 'Galaxy', 'file_name': 'a', 'file_size': 1, 'total_files': 1}));
    expect(keep.wanted()!.text, 'Galaxy veut vous envoyer des fichiers');
    keep.dispose();
  });

  test('a network that changes: the core gets the interfaces again', () async {
    final core = FakeCore()..on('set_network_info', (_) => const FakeAnswer());
    final net = _Network();
    Connection(core, network: net);
    net.changed.add(null);
    await Future<void>.delayed(Duration.zero);
    await Future<void>.delayed(Duration.zero);
    expect(core.calls.single.action, 'set_network_info');
  });
}
