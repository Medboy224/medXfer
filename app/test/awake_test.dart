// #256: the device kept awake while a transfer is offered or runs, and let go at its end.
import 'package:flutter_test/flutter_test.dart';
import 'package:medxfer/app_state.dart';
import 'package:medxfer/core/core.dart';
import 'package:medxfer/core/fake_core.dart';
import 'package:medxfer/platform/awake.dart';
import 'package:medxfer/platform/folders.dart';
import 'package:medxfer/transfer/history.dart';

class _TestFolders implements Folders {
  const _TestFolders();
  @override
  Future<String> dataDir() async => '/tmp/medxfer-test/data';
  @override
  Future<String> downloadDir() async => '/tmp/medxfer-test/out';
}

class _Awake implements Awake {
  final calls = <bool>[];

  @override
  Future<void> keep(bool on) async => calls.add(on);
}

void main() {
  test('#256: kept awake from the offer to the end of the transfer, once each way', () async {
    final awake = _Awake();
    final state = AppState(core: FakeCore(), folders: const _TestFolders(), awake: awake, history: MemoryHistoryStore());
    addTearDown(state.dispose);
    final t = state.transfers;
    t.take(const CoreEvent('batch_offered', data: {
      'summary': 'Vacances',
      'total_files': 1,
      'total_bytes': 100,
      'items': [
        {'index': 0, 'rel_path': 'a.jpg', 'size': 100, 'status': 'pending'},
      ],
    }));
    t.take(const CoreEvent('transfer_accepted', data: {}));
    t.take(const CoreEvent('transfer_progress', data: {'batch_bytes': 50, 'batch_total_bytes': 100, 'speed_mbps': 1.0}));
    expect(awake.calls, [true]);
    t.take(const CoreEvent('transfer_complete', data: {
      'items': [
        {'index': 0, 'rel_path': 'a.jpg', 'size': 100, 'status': 'completed'},
      ],
    }));
    expect(awake.calls, [true, false]);
  });
}
