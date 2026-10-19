/// The Connect screen (§1.2 of the J6b plan): this device's QR code and PIN, the ways to join
/// another device, and the devices on the same Wi-Fi. The hotspot and the camera come with
/// WP-B.2b. Once paired, the screen closes: the banner of the home screen takes over.
library;

import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app.dart';
import '../../connection/connection.dart';
import '../../l10n/app_localizations.dart';
import '../../theme.dart';
import '../../widgets/qr_view.dart';
import 'dialogs.dart';

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
      Navigator.of(context).maybePop();
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
    if (link != null) await _c.pairUri(link);
  }

  void _scanQr() {
    // The camera comes with WP-B.2b (QrScanner, decision J5).
    ScaffoldMessenger.of(context)
      ..clearSnackBars()
      ..showSnackBar(SnackBar(content: Text(AppLocalizations.of(context).comingSoon)));
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    return Scaffold(
      appBar: AppBar(title: Text(l.connectTitle)),
      body: ListenableBuilder(
        listenable: _c,
        builder: (context, _) {
          final code = _CodeCard(connection: _c);
          final join = _JoinCard(onScan: Platform.isAndroid ? _scanQr : null, onPaste: _pasteLink, onPin: () => _askPin(null));
          final nearby = _NearbyCard(
            connection: _c,
            onPick: (d) {
              _pinFor = d.name;
              unawaited(_c.pairWith(d));
            },
          );
          final remember = SwitchListTile(
            key: const Key('remember'),
            value: _c.remember,
            onChanged: (v) => unawaited(_c.setRemember(v)),
            title: Text(l.rememberDevice),
            subtitle: Text(l.rememberHint),
          );
          return LayoutBuilder(
            builder: (context, box) {
              if (box.maxWidth >= 760) {
                return Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    SizedBox(
                      width: 360,
                      child: ListView(padding: const EdgeInsets.all(16), children: [code, remember]),
                    ),
                    Expanded(
                      child: ListView(padding: const EdgeInsets.all(16), children: [join, const SizedBox(height: 16), nearby]),
                    ),
                  ],
                );
              }
              return ListView(
                padding: const EdgeInsets.all(16),
                children: [code, remember, const SizedBox(height: 8), join, const SizedBox(height: 16), nearby],
              );
            },
          );
        },
      ),
    );
  }
}

class _Section extends StatelessWidget {
  const _Section({required this.title, required this.child, this.trailing});

  final String title;
  final Widget child;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    // A Material, not a coloured box: the list tiles inside paint their ink on it.
    return Material(
      color: c.surface,
      shape: RoundedRectangleBorder(
        side: BorderSide(color: c.border),
        borderRadius: BorderRadius.circular(16),
      ),
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 12, 8, 12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(title, style: const TextStyle(fontWeight: FontWeight.w800, fontSize: 16)),
                ),
                ?trailing,
              ],
            ),
            const SizedBox(height: 8),
            Padding(padding: const EdgeInsets.only(right: 8), child: child),
          ],
        ),
      ),
    );
  }
}

/// This device's QR code and PIN, renewed when they expire.
class _CodeCard extends StatelessWidget {
  const _CodeCard({required this.connection});

  final Connection connection;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final inv = connection.invitation;
    final pin = connection.pin;
    return _Section(
      title: l.showMyCode,
      child: Column(
        children: [
          Text(
            l.showMyCodeHint,
            textAlign: TextAlign.center,
            style: TextStyle(color: c.muted),
          ),
          const SizedBox(height: 12),
          SizedBox(
            height: 220,
            child: inv == null || inv.uri.isEmpty ? const Center(child: CircularProgressIndicator()) : QrView(key: const Key('my-qr'), data: inv.uri),
          ),
          const SizedBox(height: 12),
          if (pin != null && pin.pin.isNotEmpty)
            SelectableText(
              pin.pin,
              key: const Key('my-pin'),
              style: const TextStyle(fontSize: 34, fontWeight: FontWeight.w800, letterSpacing: 6),
            ),
          // With the PIN, a device that does not see this one in its list types this address.
          if (pin != null && pin.pin.isNotEmpty && pin.address.isNotEmpty)
            SelectableText(l.pinAddress(pin.address), key: const Key('my-address'), style: TextStyle(color: c.muted)),
          if (pin != null && pin.stopped)
            Text(
              l.pinStopped,
              textAlign: TextAlign.center,
              style: TextStyle(color: c.danger),
            ),
          if (inv != null && inv.instance.isNotEmpty) Text(l.appearsAs(inv.instance), style: TextStyle(color: c.muted)),
          if (inv != null) _Countdown(until: inv.expires, onExpired: () => unawaited(connection.showCode())),
          Wrap(
            alignment: WrapAlignment.center,
            children: [
              TextButton.icon(onPressed: () => unawaited(connection.showCode()), icon: const Icon(Icons.refresh_rounded), label: Text(l.newCode)),
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
      ),
    );
  }
}

/// "Valid for n s", then [onExpired] once. Its own widget: the QR code is not redrawn each
/// second.
class _Countdown extends StatefulWidget {
  const _Countdown({required this.until, required this.onExpired});

  final DateTime until;
  final VoidCallback onExpired;

  @override
  State<_Countdown> createState() => _CountdownState();
}

class _CountdownState extends State<_Countdown> {
  Timer? _tick;
  bool _fired = false;

  @override
  void initState() {
    super.initState();
    _tick = Timer.periodic(const Duration(seconds: 1), (_) => _check());
  }

  @override
  void didUpdateWidget(_Countdown old) {
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

class _JoinCard extends StatelessWidget {
  const _JoinCard({required this.onPaste, required this.onPin, this.onScan});

  final VoidCallback? onScan;
  final VoidCallback onPaste;
  final VoidCallback onPin;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    return _Section(
      title: l.joinTitle,
      child: Wrap(
        spacing: 8,
        runSpacing: 8,
        children: [
          if (onScan != null) FilledButton.tonalIcon(onPressed: onScan, icon: const Icon(Icons.qr_code_scanner_rounded), label: Text(l.scanQr)),
          FilledButton.tonalIcon(key: const Key('paste-link'), onPressed: onPaste, icon: const Icon(Icons.link_rounded), label: Text(l.pasteLink)),
          FilledButton.tonalIcon(key: const Key('type-pin'), onPressed: onPin, icon: const Icon(Icons.pin_outlined), label: Text(l.typePin)),
        ],
      ),
    );
  }
}

class _NearbyCard extends StatelessWidget {
  const _NearbyCard({required this.connection, required this.onPick});

  final Connection connection;
  final ValueChanged<NearbyDevice> onPick;

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    final devices = connection.nearby;
    return _Section(
      title: l.nearbyTitle,
      trailing: connection.scanning
          ? const Padding(
              padding: EdgeInsets.all(12),
              child: SizedBox.square(dimension: 20, child: CircularProgressIndicator(strokeWidth: 2)),
            )
          : IconButton(tooltip: l.refresh, icon: const Icon(Icons.refresh_rounded), onPressed: () => unawaited(connection.scan())),
      child: devices.isEmpty
          ? Padding(
              padding: const EdgeInsets.symmetric(vertical: 12),
              child: Text(connection.scanning ? l.searching : l.nearbyEmpty, style: TextStyle(color: c.muted)),
            )
          : Column(
              children: [
                for (final d in devices)
                  ListTile(
                    key: ValueKey('nearby-${d.ip}'),
                    contentPadding: EdgeInsets.zero,
                    leading: CircleAvatar(
                      backgroundColor: c.chip,
                      child: Icon(d.trusted ? Icons.verified_user_rounded : Icons.devices_rounded, color: Theme.of(context).colorScheme.primary),
                    ),
                    title: Text(d.name, style: const TextStyle(fontWeight: FontWeight.w700)),
                    subtitle: Text(d.trusted ? '${l.trustedBadge} · ${d.ip}' : d.ip),
                    trailing: const Icon(Icons.chevron_right_rounded),
                    onTap: () => onPick(d),
                  ),
              ],
            ),
    );
  }
}
