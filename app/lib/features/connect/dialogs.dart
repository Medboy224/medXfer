/// The small windows of pairing: type a PIN, paste a link.
library;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app.dart';
import '../../connection/pair_link.dart';
import '../../l10n/app_localizations.dart';

/// Asks for the other device's PIN, and its address when [target] is not known yet. Returns
/// (target, pin), or null.
Future<(String, String)?> askPin(BuildContext context, {String? target, String? device}) {
  return showDialog<(String, String)>(context: context, builder: (_) => _PinDialog(target: target, device: device));
}

class _PinDialog extends StatefulWidget {
  const _PinDialog({this.target, this.device});

  final String? target;
  final String? device;

  @override
  State<_PinDialog> createState() => _PinDialogState();
}

class _PinDialogState extends State<_PinDialog> {
  final _address = TextEditingController();
  final _pin = TextEditingController();

  @override
  void dispose() {
    _address.dispose();
    _pin.dispose();
    super.dispose();
  }

  bool get _ready {
    final digits = _pin.text.replaceAll(RegExp(r'[^0-9]'), '');
    return digits.length >= 6 && (widget.target != null || _address.text.trim().isNotEmpty);
  }

  void _submit() {
    if (!_ready) return;
    Navigator.of(context).pop((widget.target ?? _address.text.trim(), _pin.text));
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    return AlertDialog(
      title: Text(l.pinDialogTitle),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (widget.device != null) Text(l.pinDialogBody(widget.device!)),
          if (widget.target == null)
            TextField(
              key: const Key('pin-address'),
              controller: _address,
              autofocus: true,
              keyboardType: TextInputType.url,
              decoration: InputDecoration(labelText: l.addressField, hintText: '192.168.1.20'),
              onChanged: (_) => setState(() {}),
            ),
          const SizedBox(height: 8),
          TextField(
            key: const Key('pin-field'),
            controller: _pin,
            autofocus: widget.target != null,
            keyboardType: TextInputType.number,
            inputFormatters: [FilteringTextInputFormatter.allow(RegExp(r'[0-9 ]')), LengthLimitingTextInputFormatter(9)],
            style: const TextStyle(fontSize: 28, fontWeight: FontWeight.w800, letterSpacing: 4),
            decoration: InputDecoration(labelText: l.pinField, hintText: '000 000'),
            onChanged: (_) => setState(() {}),
            onSubmitted: (_) => _submit(),
          ),
        ],
      ),
      actions: [
        TextButton(onPressed: () => Navigator.of(context).pop(), child: Text(l.cancel)),
        FilledButton(key: const Key('pin-ok'), onPressed: _ready ? _submit : null, child: Text(l.connect)),
      ],
    );
  }
}

/// Asks for the link the other device shows under its QR code (desktops have no camera to
/// scan it). Returns the link, or null.
Future<String?> askLink(BuildContext context) async {
  final clip = await Clipboard.getData(Clipboard.kTextPlain);
  final pasted = clip?.text?.trim() ?? '';
  if (!context.mounted) return null;
  return showDialog<String>(
    context: context,
    builder: (_) => _LinkDialog(initial: pasted.startsWith('medxfer://') ? pasted : ''),
  );
}

class _LinkDialog extends StatefulWidget {
  const _LinkDialog({required this.initial});

  final String initial;

  @override
  State<_LinkDialog> createState() => _LinkDialogState();
}

class _LinkDialogState extends State<_LinkDialog> {
  late final _link = TextEditingController(text: widget.initial);

  @override
  void dispose() {
    _link.dispose();
    super.dispose();
  }

  bool get _ready => _link.text.trim().startsWith('medxfer://');

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    return AlertDialog(
      title: Text(l.pasteLinkTitle),
      content: TextField(
        key: const Key('link-field'),
        controller: _link,
        autofocus: true,
        minLines: 1,
        maxLines: 3,
        decoration: InputDecoration(hintText: l.pasteLinkHint),
        onChanged: (_) => setState(() {}),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.of(context).pop(), child: Text(l.cancel)),
        FilledButton(
          key: const Key('link-ok'),
          onPressed: _ready ? () => Navigator.of(context).pop(_link.text.trim()) : null,
          child: Text(l.connect),
        ),
      ],
    );
  }
}

/// Pairs by a scanned or pasted link. A link that names an access point is joined by the app
/// on Android; on a desktop, the user is asked to join it in the system's settings first.
Future<void> joinByLink(BuildContext context, String text) async {
  final connection = AppScope.of(context, listen: false).state.connection;
  final l = AppLocalizations.of(context);
  final link = PairLink.parse(text);
  if (link == null) {
    ScaffoldMessenger.of(context)
      ..clearSnackBars()
      ..showSnackBar(SnackBar(content: Text(l.notALink)));
    return;
  }
  final n = link.network;
  if (n != null && !connection.hotspot.supported) {
    final go = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(l.joinWifiTitle),
        content: SelectableText(l.joinWifiBody(n.ssid, n.passphrase)),
        actions: [
          TextButton(onPressed: () => Navigator.of(context).pop(false), child: Text(l.cancel)),
          FilledButton(key: const Key('join-wifi-ok'), onPressed: () => Navigator.of(context).pop(true), child: Text(l.continueLabel)),
        ],
      ),
    );
    if (go != true) return;
  }
  await connection.pairLink(link);
}

/// Opens the camera, then pairs with what it read.
Future<void> scanAndJoin(BuildContext context) async {
  final text = await AppScope.of(context, listen: false).state.qrScanner.scan(context);
  if (text != null && context.mounted) await joinByLink(context, text);
}
