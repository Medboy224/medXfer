/// The network as the platform sees it (DISC-10, DISC-03). On Android the core cannot list the
/// interfaces (G2A-09): the app gives them with set_network_info, and holds the multicast lock
/// while it looks for devices. The desktops need neither: the core enumerates by itself.
library;

import 'dart:async';
import 'dart:io';

import 'folders.dart' show platformChannel;

/// One interface, as set_network_info takes it.
class NetInterface {
  const NetInterface({required this.name, this.index = 0, required this.addrs});

  factory NetInterface.fromMap(Map<Object?, Object?> m) => NetInterface(
        name: '${m['name']}',
        index: (m['index'] as num?)?.toInt() ?? 0,
        addrs: [for (final a in (m['addrs'] as List?) ?? const []) '$a'],
      );

  final String name;
  final int index;

  /// IPv4 with prefix: "192.168.1.20/24".
  final List<String> addrs;

  Map<String, Object?> toJson() => {'name': name, if (index > 0) 'index': index, 'addrs': addrs};
}

abstract interface class NetworkInfo {
  /// The interfaces to give the core, or null: the core enumerates by itself.
  Future<List<NetInterface>?> interfaces();

  /// Lets multicast in (mDNS answers) while devices are looked for.
  Future<void> multicast(bool on);

  /// A network came, went or changed (Android): the interfaces are to be given again.
  Stream<void> get changes;
}

/// The desktops: nothing to give, nothing to hold.
class NoNetworkInfo implements NetworkInfo {
  const NoNetworkInfo();

  @override
  Future<List<NetInterface>?> interfaces() async => null;

  @override
  Future<void> multicast(bool on) async {}

  @override
  Stream<void> get changes => const Stream.empty();
}

/// Android, through the app's Kotlin (Network.kt).
class AndroidNetworkInfo implements NetworkInfo {
  const AndroidNetworkInfo();

  @override
  Future<List<NetInterface>?> interfaces() async {
    final list = await platformChannel.invokeListMethod<Map<Object?, Object?>>('networkInterfaces');
    return [for (final m in list ?? const <Map<Object?, Object?>>[]) NetInterface.fromMap(m)];
  }

  @override
  Future<void> multicast(bool on) => platformChannel.invokeMethod<void>('multicast', {'on': on});

  static final StreamController<void> _changes = StreamController<void>.broadcast(
    onListen: () => platformChannel.setMethodCallHandler((call) async {
      if (call.method == 'networkChanged') _changes.add(null);
    }),
  );

  /// Network.kt calls networkChanged on the app's channel, once things settle.
  @override
  Stream<void> get changes => _changes.stream;
}

NetworkInfo systemNetworkInfo() => Platform.isAndroid ? const AndroidNetworkInfo() : const NoNetworkInfo();
