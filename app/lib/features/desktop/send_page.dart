/// Envoyer, on a desktop (mockup « 10. Bureau »): files and folders dropped on the window or
/// chosen, as many times as wanted, fill one list, sent in one transfer (WP-B.8: the system's
/// dialog chooses files or a folder, never both). Then the device: the one connected, a trusted
/// one (connected, then sent to, without a code), or another one to pair with by its PIN.
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../app_state.dart';
import '../../connection/connection.dart';
import '../../l10n/app_localizations.dart';
import '../../library/desktop_picks.dart';
import '../../library/selection.dart';
import '../../theme.dart';
import '../../widgets/dashed_box.dart';
import '../../widgets/drop_zone.dart';
import '../../widgets/format.dart';
import '../connect/connect_screen.dart';
import '../connect/dialogs.dart';

class SendPage extends StatefulWidget {
  const SendPage({super.key});

  @override
  State<SendPage> createState() => _SendPageState();
}

class _SendPageState extends State<SendPage> {
  late final AppState _state = AppScope.of(context, listen: false).state;
  Connection get _c => _state.connection;
  Timer? _rescan;
  bool _asking = false;
  String? _pinFor;

  /// A device chosen to send to, not connected yet: the list goes once it is.
  bool _sendOnSession = false;

  @override
  void initState() {
    super.initState();
    _c.addListener(_changed);
    // After the first frame: the scan tells the listeners at once, and this page builds now.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      unawaited(_c.loadTrusted());
      unawaited(_c.scan());
    });
    // The devices come and go: look again now and then, while the page is open.
    _rescan = Timer.periodic(const Duration(seconds: 8), (_) {
      if (!_c.scanning) unawaited(_c.scan());
    });
  }

  @override
  void dispose() {
    _rescan?.cancel();
    _c.removeListener(_changed);
    super.dispose();
  }

  void _changed() {
    if (!mounted) return;
    if (_c.session != null) {
      if (_sendOnSession) {
        _sendOnSession = false;
        unawaited(_send());
      }
      return;
    }
    // Not under another screen (Connect asks the PIN itself).
    final target = _c.pinNeeded;
    if (target != null && !_asking && (ModalRoute.of(context)?.isCurrent ?? true)) unawaited(_askPin(target));
  }

  Future<void> _askPin(String target) async {
    _asking = true;
    final answer = await askPin(context, target: target, device: _pinFor ?? target);
    _asking = false;
    _pinFor = null;
    if (answer == null) {
      _sendOnSession = false;
      _c.cancelPinNeeded();
      return;
    }
    await _c.pairPin(answer.$1, answer.$2);
  }

  void _add(List<String> paths) {
    _state.selection.addAll(picksOfPaths(paths));
    _state.explorer.sizeFolders(_state.selection);
  }

  Future<void> _chooseFiles() async => _add((await _state.filePicker.files()).paths);

  Future<void> _chooseFolder() async {
    final folder = await _state.filePicker.folder();
    if (folder != null) _add([folder]);
  }

  /// Everything chosen, by path, in one transfer.
  Future<void> _send() async {
    final sel = _state.selection;
    if (sel.isEmpty) return;
    final plan = sel.plan(byPath: true) as ByPath;
    await _state.transfers.send(plan.paths);
    sel.clear();
  }

  /// A device not connected: connected first (without a code if trusted), then sent to.
  void _reach(NearbyDevice d) {
    _pinFor = d.name;
    _sendOnSession = !_state.selection.isEmpty;
    unawaited(_c.pairWith(d));
  }

  void _showCode() => unawaited(
    showDialog<void>(
      context: context,
      builder: (_) => Dialog(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 420),
          child: Padding(
            padding: const EdgeInsets.only(top: 24),
            child: ManualCodeSheet(connection: _c),
          ),
        ),
      ),
    ),
  );

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    return DropZone(
      hint: l.dropHere,
      onPaths: _add,
      child: ListenableBuilder(
        listenable: Listenable.merge([_c, _state.selection]),
        builder: (context, _) => LayoutBuilder(
          builder: (context, box) {
            final pick = _Pick(onFiles: _chooseFiles, onFolder: _chooseFolder, selection: _state.selection);
            final to = _SendTo(connection: _c, canSend: !_state.selection.isEmpty, onSend: _send, onReach: _reach, onShowCode: _showCode);
            return SingleChildScrollView(
              padding: const EdgeInsets.all(24),
              child: box.maxWidth >= 860
                  ? Row(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Expanded(flex: 3, child: pick),
                        const SizedBox(width: 24),
                        Expanded(flex: 2, child: to),
                      ],
                    )
                  : Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [pick, const SizedBox(height: 24), to]),
            );
          },
        ),
      ),
    );
  }
}

/// The left column: where to drop or choose, then the list of what was chosen.
class _Pick extends StatelessWidget {
  const _Pick({required this.onFiles, required this.onFolder, required this.selection});

  final VoidCallback onFiles;
  final VoidCallback onFolder;
  final Selection selection;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final accent = Theme.of(context).colorScheme.primary;
    final choose = OutlinedButton.styleFrom(
      minimumSize: const Size(0, 44),
      padding: const EdgeInsets.symmetric(horizontal: 18),
      shape: const StadiumBorder(),
      backgroundColor: c.background,
      foregroundColor: c.text,
      side: BorderSide(color: c.border),
      textStyle: const TextStyle(fontFamily: 'Figtree', fontWeight: FontWeight.w600),
    );
    final picks = selection.items.toList();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(l.navSend, style: const TextStyle(fontSize: 24, fontWeight: FontWeight.w800)),
        const SizedBox(height: 16),
        DashedBox(
          key: const Key('drop-card'),
          color: accent.withValues(alpha: 0.4),
          fill: c.surface,
          child: Padding(
            padding: const EdgeInsets.symmetric(vertical: 40, horizontal: 24),
            child: Column(
              children: [
                Icon(Icons.upload_rounded, size: 40, color: accent),
                const SizedBox(height: 12),
                Text(
                  l.dropTitle,
                  textAlign: TextAlign.center,
                  style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w700),
                ),
                const SizedBox(height: 12),
                Text(
                  l.dropBody,
                  textAlign: TextAlign.center,
                  style: TextStyle(fontSize: 14, color: c.muted),
                ),
                const SizedBox(height: 12),
                Wrap(
                  alignment: WrapAlignment.center,
                  spacing: 8,
                  runSpacing: 8,
                  children: [
                    OutlinedButton(key: const Key('pick-files'), style: choose, onPressed: onFiles, child: Text(l.chooseFiles)),
                    OutlinedButton(key: const Key('pick-folder'), style: choose, onPressed: onFolder, child: Text(l.chooseFolder)),
                  ],
                ),
              ],
            ),
          ),
        ),
        if (picks.isNotEmpty) ...[
          const SizedBox(height: 16),
          Material(
            key: const Key('basket'),
            color: c.surface,
            clipBehavior: Clip.antiAlias,
            shape: RoundedRectangleBorder(
              side: BorderSide(color: c.border),
              borderRadius: BorderRadius.circular(18),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 4, 8, 4),
                  child: Row(
                    children: [
                      Expanded(
                        child: Text(
                          l.basketLine(selection.count, formatBytes(context, selection.bytes)),
                          key: const Key('basket-line'),
                          style: const TextStyle(fontWeight: FontWeight.w700),
                        ),
                      ),
                      TextButton(key: const Key('basket-clear'), onPressed: selection.clear, child: Text(l.removeAll)),
                    ],
                  ),
                ),
                for (final (i, p) in picks.indexed) ...[
                  Divider(height: 1, color: c.border),
                  Padding(
                    padding: const EdgeInsets.fromLTRB(16, 4, 4, 4),
                    child: Row(
                      children: [
                        Icon(p.kind == PickKind.folder ? Icons.folder_outlined : Icons.insert_drive_file_outlined, size: 20, color: c.muted),
                        const SizedBox(width: 12),
                        Expanded(
                          child: Text(
                            p.name,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 14),
                          ),
                        ),
                        Text(p.kind == PickKind.folder && p.size == 0 ? '…' : formatBytes(context, p.size), style: TextStyle(fontSize: 13, color: c.muted)),
                        IconButton(
                          key: ValueKey('basket-remove-$i'),
                          tooltip: l.removeOne,
                          icon: Icon(Icons.close_rounded, size: 18, color: c.muted),
                          onPressed: () => selection.toggle(p),
                        ),
                      ],
                    ),
                  ),
                ],
              ],
            ),
          ),
        ],
      ],
    );
  }
}

/// The right column: the devices to send to, then a way to be found by one not listed.
class _SendTo extends StatelessWidget {
  const _SendTo({required this.connection, required this.canSend, required this.onSend, required this.onReach, required this.onShowCode});

  final Connection connection;
  final bool canSend;
  final VoidCallback onSend;
  final ValueChanged<NearbyDevice> onReach;
  final VoidCallback onShowCode;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final session = connection.session;
    // The device connected first, then the others found on the network.
    final others = [
      for (final d in connection.nearby)
        if (session == null || (d.ip != session.ip && d.name != session.name)) d,
    ];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            Expanded(
              child: Text(l.sendTo, style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w800)),
            ),
            connection.scanning
                ? const Padding(
                    padding: EdgeInsets.all(12),
                    child: SizedBox.square(dimension: 18, child: CircularProgressIndicator(strokeWidth: 2)),
                  )
                : IconButton(tooltip: l.refresh, icon: const Icon(Icons.refresh_rounded), onPressed: () => unawaited(connection.scan())),
          ],
        ),
        const SizedBox(height: 4),
        if (session != null)
          _Device(
            key: const Key('device-session'),
            name: session.name,
            detail: connection.reconnecting ? l.reconnecting : l.deviceConnected,
            action: l.sendSelection,
            primary: true,
            onAction: canSend ? onSend : null,
          ),
        for (final d in others)
          _Device(
            key: ValueKey('device-${d.ip}'),
            name: d.name,
            detail: d.trusted ? l.deviceTrustedDirect : l.devicePairByCode,
            // Trusted: sent to at once, or connected if nothing is chosen yet.
            action: d.trusted ? (canSend ? l.sendSelection : l.connect) : l.pairAction,
            primary: d.trusted,
            onAction: () => onReach(d),
          ),
        if (session == null && others.isEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 8),
            child: Text(connection.scanning ? l.searching : l.nearbyEmpty, style: TextStyle(color: c.muted)),
          ),
        const SizedBox(height: 4),
        OutlinedButton(
          key: const Key('show-code'),
          style: OutlinedButton.styleFrom(
            minimumSize: const Size(0, 44),
            shape: const StadiumBorder(),
            foregroundColor: c.text,
            side: BorderSide(color: c.border),
            textStyle: const TextStyle(fontFamily: 'Figtree', fontWeight: FontWeight.w600),
          ),
          onPressed: onShowCode,
          child: Text(l.showQrOrPin),
        ),
        Align(
          child: TextButton(
            key: const Key('other-ways'),
            onPressed: () => unawaited(Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => const ConnectScreen()))),
            child: Text(l.otherWays),
          ),
        ),
      ],
    );
  }
}

/// One device of the list, with its button: Envoyer, or Appairer.
class _Device extends StatelessWidget {
  const _Device({super.key, required this.name, required this.detail, required this.action, required this.primary, required this.onAction});

  final String name;
  final String detail;
  final String action;
  final bool primary;
  final VoidCallback? onAction;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    final accent = Theme.of(context).colorScheme.primary;
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: c.surface,
          border: Border.all(color: c.border),
          borderRadius: BorderRadius.circular(16),
        ),
        child: Row(
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(name, style: const TextStyle(fontWeight: FontWeight.w700, fontSize: 15)),
                  const SizedBox(height: 2),
                  Text(detail, style: TextStyle(color: c.muted, fontSize: 13)),
                ],
              ),
            ),
            const SizedBox(width: 12),
            FilledButton(
              style: FilledButton.styleFrom(
                minimumSize: const Size(0, 40),
                padding: const EdgeInsets.symmetric(horizontal: 16),
                shape: const StadiumBorder(),
                backgroundColor: primary ? accent : c.chip,
                foregroundColor: primary ? Colors.white : c.text,
                textStyle: const TextStyle(fontFamily: 'Figtree', fontWeight: FontWeight.w700),
              ),
              onPressed: onAction,
              child: Text(action),
            ),
          ],
        ),
      ),
    );
  }
}
