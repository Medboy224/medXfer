/// The Connect screen (§1.2 of the J6b plan, mockup « Connecter »): three ways, in the order of
/// the mockup. Create an access point (Android, WP-B.7): its QR code carries the network, so
/// scanning it works wherever the devices are. Join a device that created one: scan its QR code
/// or type its PIN. Or pick a device already on the same Wi-Fi. No QR code of the shared network
/// here: one scanned elsewhere would fail. A device missing from the list is reached by its
/// address and PIN. Once paired, the screen closes: the banner of the home screen takes over.
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app.dart';
import '../../connection/connection.dart';
import '../../l10n/app_localizations.dart';
import '../../theme.dart';
import 'dialogs.dart';
import 'hotspot_screen.dart';

class ConnectScreen extends StatefulWidget {
  const ConnectScreen({super.key});

  @override
  State<ConnectScreen> createState() => _ConnectScreenState();
}

class _ConnectScreenState extends State<ConnectScreen> {
  late final Connection _c;
  Timer? _rescan;
  String? _pinFor; // the name of the device whose PIN is asked for
  bool _asking = false;

  @override
  void initState() {
    super.initState();
    _c = AppScope.of(context, listen: false).state.connection;
    _c.addListener(_changed);
    unawaited(_c.openConnect());
    // The devices come and go: look again now and then, while the screen is open.
    _rescan = Timer.periodic(const Duration(seconds: 8), (_) {
      if (!_c.scanning) unawaited(_c.scan());
    });
  }

  @override
  void dispose() {
    _rescan?.cancel();
    _c.removeListener(_changed);
    // Not during the unmount: the model tells its listeners, and the tree is locked now.
    scheduleMicrotask(() => unawaited(_c.closeConnect()));
    super.dispose();
  }

  void _changed() {
    if (!mounted) return;
    if (_c.session != null) {
      // This screen's own route: the top one may be a window closing (the PIN's), and popping
      // it would leave this screen open.
      final route = ModalRoute.of(context);
      if (route != null && route.isActive) {
        route.isCurrent ? Navigator.of(context).pop() : Navigator.of(context).removeRoute(route);
      }
      return;
    }
    final target = _c.pinNeeded;
    if (target != null && !_asking) unawaited(_askPin(target));
  }

  Future<void> _askPin(String? target) async {
    _asking = true;
    final answer = await askPin(context, target: target, device: _pinFor ?? target);
    _asking = false;
    _pinFor = null;
    if (answer == null) {
      _c.cancelPinNeeded();
      return;
    }
    await _c.pairPin(answer.$1, answer.$2);
  }

  Future<void> _pasteLink() async {
    final link = await askLink(context);
    if (link != null && mounted) await joinByLink(context, link);
  }

  void _openHotspot() => unawaited(Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => const HotspotScreen())));

  void _openManual() => unawaited(
    showModalBottomSheet<void>(context: context, isScrollControlled: true, showDragHandle: true, builder: (_) => ManualCodeSheet(connection: _c)),
  );

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final scanner = AppScope.of(context).state.qrScanner;
    return Scaffold(
      appBar: AppBar(title: Text(l.connectTitle)),
      body: ListenableBuilder(
        listenable: _c,
        builder: (context, _) => Align(
          alignment: Alignment.topCenter,
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 560),
            child: ListView(
              padding: const EdgeInsets.all(16),
              children: [
                if (_c.hotspot.supported) ...[_HotspotCard(busy: _c.hotspotBusy, onOpen: _openHotspot), const SizedBox(height: 14)],
                _JoinCard(
                  onScan: scanner.supported ? () => scanAndJoin(context) : null,
                  // Without a camera (a desktop), the link copied on the other device.
                  onPaste: scanner.supported ? null : _pasteLink,
                  onPin: () => _askPin(null),
                ),
                const SizedBox(height: 20),
                _Nearby(
                  connection: _c,
                  onPick: (d) {
                    _pinFor = d.name;
                    unawaited(_c.pairWith(d));
                  },
                  onNotListed: _openManual,
                ),
                const SizedBox(height: 8),
                SwitchListTile(
                  key: const Key('remember'),
                  value: _c.remember,
                  onChanged: (v) => unawaited(_c.setRemember(v)),
                  title: Text(l.rememberDevice),
                  subtitle: Text(l.rememberHint),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// A card of the screen, with its title.
class _Section extends StatelessWidget {
  const _Section({required this.title, required this.child});

  final String title;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    // A Material, not a coloured box: the buttons inside paint their ink on it.
    return Material(
      color: c.surface,
      shape: RoundedRectangleBorder(side: BorderSide(color: c.border), borderRadius: BorderRadius.circular(22)),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(title, style: const TextStyle(fontWeight: FontWeight.w800, fontSize: 18)),
            const SizedBox(height: 4),
            child,
          ],
        ),
      ),
    );
  }
}

/// Create an access point: the first way of the mockup, in the accent colour.
class _HotspotCard extends StatelessWidget {
  const _HotspotCard({required this.busy, required this.onOpen});

  final bool busy;
  final VoidCallback onOpen;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final accent = Theme.of(context).colorScheme.primary;
    return Material(
      color: accent,
      borderRadius: BorderRadius.circular(22),
      child: InkWell(
        key: const Key('hotspot-start'),
        borderRadius: BorderRadius.circular(22),
        onTap: busy ? null : onOpen,
        child: Padding(
          padding: const EdgeInsets.all(18),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  CircleAvatar(
                    radius: 24,
                    backgroundColor: Colors.white.withValues(alpha: 0.18),
                    child: const Icon(Icons.wifi_tethering_rounded, color: Colors.white, size: 26),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Text(l.hotspotStart, style: const TextStyle(color: Colors.white, fontSize: 18, fontWeight: FontWeight.w800)),
                  ),
                ],
              ),
              const SizedBox(height: 10),
              Text(l.hotspotCardHint, style: TextStyle(color: Colors.white.withValues(alpha: 0.92), fontSize: 14, height: 1.4)),
            ],
          ),
        ),
      ),
    );
  }
}

/// Join a device that created an access point: its QR code, or its Wi-Fi then its PIN.
class _JoinCard extends StatelessWidget {
  const _JoinCard({required this.onPin, this.onScan, this.onPaste});

  final VoidCallback? onScan;
  final VoidCallback? onPaste;
  final VoidCallback onPin;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    return _Section(
      title: l.joinTitle,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(l.joinHint, style: TextStyle(color: c.muted, height: 1.4)),
          const SizedBox(height: 12),
          Row(
            children: [
              if (onScan != null)
                Expanded(
                  child: FilledButton.tonalIcon(key: const Key('scan-qr'), onPressed: onScan, icon: const Icon(Icons.qr_code_scanner_rounded), label: Text(l.scanQrShort)),
                ),
              if (onPaste != null)
                Expanded(
                  child: FilledButton.tonalIcon(key: const Key('paste-link'), onPressed: onPaste, icon: const Icon(Icons.link_rounded), label: Text(l.pasteLink)),
                ),
              const SizedBox(width: 8),
              Expanded(
                child: FilledButton.tonalIcon(key: const Key('type-pin'), onPressed: onPin, icon: const Icon(Icons.pin_outlined), label: Text(l.typePin)),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

/// The name of a kind of path, as the screens show it (DISC-13).
String pathLabel(AppLocalizations l, PathKind kind) => switch (kind) {
  PathKind.cable => l.pathCable,
  PathKind.wifi => l.pathWifi,
  PathKind.other => l.pathOther,
};

/// The devices already on the same Wi-Fi, under the name this one appears as.
class _Nearby extends StatelessWidget {
  const _Nearby({required this.connection, required this.onPick, required this.onNotListed});

  final Connection connection;
  final ValueChanged<NearbyDevice> onPick;
  final VoidCallback onNotListed;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final accent = Theme.of(context).colorScheme.primary;
    final devices = connection.nearby;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            Expanded(
              child: Padding(
                padding: const EdgeInsets.only(left: 4),
                child: Text(
                  l.nearbyTitle.toUpperCase(),
                  style: TextStyle(color: c.muted, fontSize: 13, fontWeight: FontWeight.w700, letterSpacing: 0.8),
                ),
              ),
            ),
            connection.scanning
                ? const Padding(padding: EdgeInsets.all(12), child: SizedBox.square(dimension: 18, child: CircularProgressIndicator(strokeWidth: 2)))
                : IconButton(tooltip: l.refresh, icon: const Icon(Icons.refresh_rounded), onPressed: () => unawaited(connection.scan())),
          ],
        ),
        // The name the other user looks for in their list (DISC-07): a device not yet trusted
        // never sees this one's real name.
        if (connection.instance.isNotEmpty)
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 0, 4, 10),
            child: Text(l.youAppearAs(connection.instance), key: const Key('my-instance'), style: const TextStyle(fontWeight: FontWeight.w600)),
          ),
        if (devices.isEmpty)
          Padding(
            padding: const EdgeInsets.fromLTRB(4, 4, 4, 8),
            child: Text(connection.scanning ? l.searching : l.nearbyEmpty, style: TextStyle(color: c.muted)),
          ),
        for (final d in devices)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: Material(
              color: c.surface,
              shape: RoundedRectangleBorder(side: BorderSide(color: c.border), borderRadius: BorderRadius.circular(16)),
              child: InkWell(
                key: ValueKey('nearby-${d.ip}'),
                borderRadius: BorderRadius.circular(16),
                onTap: () => onPick(d),
                child: Padding(
                  padding: const EdgeInsets.all(12),
                  child: Row(
                    children: [
                      CircleAvatar(
                        radius: 22,
                        backgroundColor: c.chip,
                        child: Icon(d.trusted ? Icons.verified_user_rounded : Icons.devices_rounded, color: d.trusted ? accent : c.muted),
                      ),
                      const SizedBox(width: 12),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(d.name, style: const TextStyle(fontWeight: FontWeight.w700, fontSize: 16)),
                            const SizedBox(height: 2),
                            Text(d.trusted ? l.nearbyTrusted : l.nearbyAsksPin, style: TextStyle(color: c.muted, fontSize: 13)),
                          ],
                        ),
                      ),
                      d.trusted
                          ? Container(
                              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
                              decoration: BoxDecoration(color: accent, borderRadius: BorderRadius.circular(14)),
                              child: Text(l.connect, style: const TextStyle(color: Colors.white, fontSize: 13, fontWeight: FontWeight.w700)),
                            )
                          : Icon(Icons.chevron_right_rounded, color: c.muted),
                    ],
                  ),
                ),
              ),
            ),
          ),
        Align(
          alignment: Alignment.centerLeft,
          child: TextButton(key: const Key('not-listed'), onPressed: onNotListed, child: Text(l.notListed)),
        ),
      ],
    );
  }
}

/// A device that does not see this one in its list (discovery blocked by the Wi-Fi or a
/// firewall) types this address and PIN. No QR code: the two must share the network.
class ManualCodeSheet extends StatefulWidget {
  const ManualCodeSheet({super.key, required this.connection});

  final Connection connection;

  @override
  State<ManualCodeSheet> createState() => _ManualCodeSheetState();
}

class _ManualCodeSheetState extends State<ManualCodeSheet> {
  Connection get _c => widget.connection;

  @override
  void initState() {
    super.initState();
    _c.addListener(_changed);
    unawaited(_c.openCode());
  }

  @override
  void dispose() {
    _c.removeListener(_changed);
    scheduleMicrotask(_c.closeCode);
    super.dispose();
  }

  void _changed() {
    if (mounted && _c.session != null) Navigator.of(context).pop();
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(24, 0, 24, 24),
        child: ListenableBuilder(
          listenable: _c,
          builder: (context, _) {
            final pin = _c.pin;
            final inv = _c.invitation;
            return Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text(l.manualTitle, style: const TextStyle(fontSize: 20, fontWeight: FontWeight.w800)),
                const SizedBox(height: 8),
                Text(l.manualBody, style: TextStyle(color: c.muted, height: 1.4)),
                const SizedBox(height: 16),
                if (pin == null)
                  const Center(child: Padding(padding: EdgeInsets.all(16), child: CircularProgressIndicator()))
                else if (pin.stopped)
                  Text(l.pinStopped, textAlign: TextAlign.center, style: TextStyle(color: c.danger))
                else ...[
                  // One address per network (DISC-13): the other PC types the one of the network
                  // they share, the cable's or the Wi-Fi's.
                  if (pin.addresses.length > 1)
                    for (final a in pin.addresses)
                      SelectableText(
                        l.pinAddressLine(pathLabel(l, a.kind), a.address),
                        key: Key('my-address-${a.kind.name}-${a.address}'),
                        textAlign: TextAlign.center,
                        style: const TextStyle(fontSize: 17, fontWeight: FontWeight.w700),
                      )
                  else
                    SelectableText(pin.address, key: const Key('my-address'), textAlign: TextAlign.center, style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w700)),
                  const SizedBox(height: 4),
                  SelectableText(
                    pin.pin,
                    key: const Key('my-pin'),
                    textAlign: TextAlign.center,
                    style: const TextStyle(fontSize: 34, fontWeight: FontWeight.w800, letterSpacing: 6),
                  ),
                  Center(child: CodeCountdown(until: pin.expires, onExpired: () => unawaited(_c.showCode()))),
                ],
                const SizedBox(height: 8),
                Wrap(
                  alignment: WrapAlignment.center,
                  children: [
                    TextButton.icon(onPressed: () => unawaited(_c.showCode()), icon: const Icon(Icons.refresh_rounded), label: Text(l.newCode)),
                    // On a desktop, the link pasted on the other one (same network).
                    if (inv != null && inv.uri.isNotEmpty)
                      TextButton.icon(
                        onPressed: () async {
                          await Clipboard.setData(ClipboardData(text: inv.uri));
                          if (!context.mounted) return;
                          ScaffoldMessenger.of(context)
                            ..clearSnackBars()
                            ..showSnackBar(SnackBar(content: Text(l.linkCopied)));
                        },
                        icon: const Icon(Icons.copy_rounded),
                        label: Text(l.copyLink),
                      ),
                  ],
                ),
              ],
            );
          },
        ),
      ),
    );
  }
}

/// "Valid for n s", then [onExpired] once: a new code is drawn. Its own widget: the QR code is
/// not redrawn each second.
class CodeCountdown extends StatefulWidget {
  const CodeCountdown({super.key, required this.until, required this.onExpired});

  final DateTime until;
  final VoidCallback onExpired;

  @override
  State<CodeCountdown> createState() => _CodeCountdownState();
}

class _CodeCountdownState extends State<CodeCountdown> {
  Timer? _tick;
  bool _fired = false;

  @override
  void initState() {
    super.initState();
    _tick = Timer.periodic(const Duration(seconds: 1), (_) => _check());
  }

  @override
  void didUpdateWidget(CodeCountdown old) {
    super.didUpdateWidget(old);
    if (old.until != widget.until) _fired = false;
  }

  void _check() {
    if (!mounted) return;
    if (!_fired && !DateTime.now().isBefore(widget.until)) {
      _fired = true;
      widget.onExpired();
    }
    setState(() {});
  }

  @override
  void dispose() {
    _tick?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final left = widget.until.difference(DateTime.now()).inSeconds.clamp(0, 9999);
    return Text(AppLocalizations.of(context).validFor('$left'), style: TextStyle(color: context.colors.muted, fontSize: 13));
  }
}
