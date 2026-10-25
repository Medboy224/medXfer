// WP-B.10: where received files go. The folder chosen in the settings, else the default one;
// sorted by type on Android; a chosen folder needs the access to all files there, and one that
// is gone gives way to the default, said to the user.
import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/core/core.dart';
import 'package:medxfer/core/fake_core.dart';
import 'package:medxfer/platform/folders.dart';
import 'package:medxfer/platform/storage.dart';
import 'package:medxfer/settings.dart';
import 'package:medxfer/transfer/history.dart';
import 'package:medxfer/transfer/receive_folders.dart';
import 'package:medxfer/transfer/transfers.dart';

class _Folders implements Folders {
  @override
  Future<String> dataDir() async => '/data';
  @override
  Future<String> downloadDir() async => '/home/awa/Downloads/medXfer';
}

class _Android implements ReceiveStorage {
  _Android({this.all = true});
  bool all;
  @override
  bool get supported => true;
  @override
  Future<bool> allFiles() async => all;
  @override
  Future<bool> askAllFiles() async => all = true;
  @override
  Future<String> root() async => all ? '/storage/emulated/0/medXfer' : '/storage/emulated/0/Download/medXfer';
}

IncomingOffer _photos() => IncomingOffer.fromJson({
  'device_name': 'PC',
  'file_name': 'Vacances',
  'file_size': 2,
  'total_files': 2,
  'items': [
    {'index': 0, 'rel_path': 'Vacances/a.jpg', 'size': 1, 'status': 'pending'},
    {'index': 1, 'rel_path': 'Vacances/b.jpg', 'size': 1, 'status': 'pending'},
  ],
});

ReceiveFolders _folders(AppSettings s, ReceiveStorage storage, {Set<String> there = const {}}) =>
    ReceiveFolders(settings: s, storage: storage, folders: _Folders(), exists: (d) async => there.contains(d));

void main() {
  test('desktop: Downloads/medXfer by default, else the folder chosen, never sorted', () async {
    final s = AppSettings();
    final f = _folders(s, const NoReceiveStorage(), there: {'/mnt/data/recu'});
    expect((await f.placeFor(_photos())).dir, '/home/awa/Downloads/medXfer');
    s.receiveDir = '/mnt/data/recu';
    final place = await f.placeFor(_photos());
    expect((place.dir, place.gone), ('/mnt/data/recu', ''));
  });

  test('Android: medXfer/ sorted by type by default; the chosen folder, sorted or not', () async {
    final s = AppSettings();
    final f = _folders(s, _Android(), there: {'/storage/emulated/0/Reçus'});
    expect((await f.placeFor(_photos())).dir, '/storage/emulated/0/medXfer/Photos');
    s.receiveDir = '/storage/emulated/0/Reçus';
    expect((await f.placeFor(_photos())).dir, '/storage/emulated/0/Reçus/Photos');
    s.sortByType = false;
    expect((await f.placeFor(_photos())).dir, '/storage/emulated/0/Reçus');
    expect(await f.current(), '/storage/emulated/0/Reçus');
  });

  test('Android without the access to all files: Download/medXfer/, the chosen folder set aside', () async {
    final s = AppSettings()..receiveDir = '/storage/emulated/0/Reçus';
    final f = _folders(s, _Android(all: false), there: {'/storage/emulated/0/Reçus'});
    expect(await f.canChoose(), isFalse);
    final place = await f.placeFor(_photos());
    expect((place.dir, place.gone), ('/storage/emulated/0/Download/medXfer/Photos', '/storage/emulated/0/Reçus'));
  });

  test('a chosen folder that is gone: the default one, and the user is told', () async {
    final s = AppSettings()..receiveDir = '/media/usb/recu';
    final core = FakeCore()..on('respond_offer', (_) => const FakeAnswer());
    final f = _folders(s, const NoReceiveStorage());
    final t = Transfers(core, history: MemoryHistoryStore(), peerName: () => 'PC', downloadDir: () async => '/x', placeFor: f.placeFor);
    final notices = <TransferNotice>[];
    t.notices.listen(notices.add);
    t.take(CoreEvent('incoming_offer', data: {
      'device_name': 'PC',
      'file_name': 'a.jpg',
      'file_size': 1,
      'total_files': 1,
    }));
    await t.accept();
    expect(core.calls.last.payload, {'accept': true, 'save_dir': '/home/awa/Downloads/medXfer'});
    await pumpEventQueue();
    expect(notices.single, isA<ReceiveFolderGone>());
    final gone = notices.single as ReceiveFolderGone;
    expect((gone.folder, gone.used), ('/media/usb/recu', '/home/awa/Downloads/medXfer'));
  });

  test('the choice is saved with the settings', () async {
    final store = MemorySettingsStore();
    AppSettings(store: store)
      ..receiveDir = '/mnt/data/recu'
      ..sortByType = false;
    await pumpEventQueue();
    final next = AppSettings(store: store);
    await next.load();
    expect((next.receiveDir, next.sortByType), ('/mnt/data/recu', false));
  });
}
