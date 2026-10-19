/// The core of medXfer as the interface sees it (bible §10.4, API-01): it is started once,
/// takes actions, and gives events. Two adapters implement it: [FfiCore], the real one, over
/// the C library; and FakeCore, which replays recorded scenarios (DEV-17) for the tests.
library;

/// The schema of the messages this interface speaks (pkg/core SchemaVersion).
const coreSchema = 4;

/// The folders and name the core starts with (B6: the paths come from the caller).
class CoreConfig {
  const CoreConfig({required this.dataDir, required this.downloadDir, this.deviceName = ''});

  final String dataDir;
  final String downloadDir;
  final String deviceName;

  Map<String, Object?> toJson() => {
        'data_dir': dataDir,
        'download_dir': downloadDir,
        if (deviceName.isNotEmpty) 'device_name': deviceName,
      };
}

/// What starting the core said.
class CoreStart {
  const CoreStart({required this.ok, this.alreadyStarted = false, this.schema = 0, this.error});

  factory CoreStart.fromJson(Map<String, Object?> m) => CoreStart(
        ok: m['ok'] == true,
        alreadyStarted: m['already_started'] == true,
        schema: (m['schema'] as num?)?.toInt() ?? 0,
        error: m['error'] as String?,
      );

  final bool ok;

  /// A hot restart found the core running (B4).
  final bool alreadyStarted;
  final int schema;
  final String? error;
}

/// One event of the core: its name, the request it answers (if any), and its data.
class CoreEvent {
  const CoreEvent(this.name, {this.id, this.data});

  factory CoreEvent.fromJson(Map<String, Object?> m) =>
      CoreEvent(m['event'] as String? ?? '', id: m['id'] as String?, data: m['data']);

  /// The event the core gives every reader once it has stopped (B9).
  static const stopped = 'core_stopped';

  final String name;
  final String? id;
  final Object? data;

  /// data as a map, or an empty one.
  Map<String, Object?> get map => data is Map<String, Object?> ? data as Map<String, Object?> : const {};

  @override
  String toString() => 'CoreEvent($name${id == null ? '' : ', id: $id'})';
}

/// The synchronous answer to an action: the replies meant for this caller alone. Events that
/// come later, or for everyone, arrive on [Core.events].
class CoreReply {
  const CoreReply({required this.ok, this.id, this.error, this.events = const []});

  factory CoreReply.fromJson(Map<String, Object?> m) => CoreReply(
        ok: m['ok'] == true,
        id: m['id'] as String?,
        error: m['error'] as String?,
        events: [
          for (final e in (m['events'] as List?) ?? const [])
            if (e is Map<String, Object?>) CoreEvent.fromJson(e),
        ],
      );

  final bool ok;
  final String? id;
  final String? error;
  final List<CoreEvent> events;

  /// The first reply named name, if any.
  CoreEvent? first(String name) {
    for (final e in events) {
      if (e.name == name) return e;
    }
    return null;
  }
}

/// The core.
abstract interface class Core {
  /// Starts the core. A second start finds it running (B4).
  Future<CoreStart> start(CoreConfig config);

  /// Runs one action (API-02: `{id, action, payload}`).
  Future<CoreReply> call(String action, [Map<String, Object?>? payload]);

  /// Every event of the core, in order. Ends after [CoreEvent.stopped].
  Stream<CoreEvent> get events;

  /// Stops the core; a later [start] starts a fresh one.
  Future<void> stop();
}
