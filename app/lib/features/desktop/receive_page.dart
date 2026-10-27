/// Recevoir, on a desktop (decided on 27 October 2026, no mockup of its own): while the page
/// is open, this computer is announced (DISC-05: when the user asks), so the devices on the same
/// Wi-Fi find it; a QR code or PIN for the others; and where received files go.
library;

import 'dart:async';

import 'package:flutter/material.dart';

import '../../app.dart';
import '../../connection/connection.dart';
import '../../l10n/app_localizations.dart';
import '../../theme.dart';
import '../connect/connect_screen.dart';
import '../settings/settings_screen.dart';

class ReceivePage extends StatefulWidget {
  const ReceivePage({super.key});

  @override
  State<ReceivePage> createState() => _ReceivePageState();
}

class _ReceivePageState extends State<ReceivePage> {
  late final Connection _c = AppScope.of(context, listen: false).state.connection;

  @override
  void initState() {
    super.initState();
    unawaited(_c.openConnect());
  }

  @override
  void dispose() {
    // Not during the unmount: the model tells its listeners, and the tree is locked now.
    scheduleMicrotask(() => unawaited(_c.closeConnect()));
    super.dispose();
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
    final c = context.colors;
    return ListView(
      padding: const EdgeInsets.all(24),
      children: [
        Text(l.navReceive, style: const TextStyle(fontSize: 24, fontWeight: FontWeight.w800)),
        const SizedBox(height: 16),
        Material(
          color: c.surface,
          shape: RoundedRectangleBorder(
            side: BorderSide(color: c.border),
            borderRadius: BorderRadius.circular(18),
          ),
          child: Padding(
            padding: const EdgeInsets.all(20),
            child: ListenableBuilder(
              listenable: _c,
              builder: (context, _) => Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  if (_c.instance.isNotEmpty)
                    Text(
                      l.youAppearAs(_c.instance),
                      key: const Key('my-instance'),
                      style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w700),
                    ),
                  const SizedBox(height: 6),
                  Text(l.receiveVisible, style: TextStyle(color: c.muted, height: 1.4)),
                  const SizedBox(height: 16),
                  OutlinedButton(
                    key: const Key('show-code'),
                    style: OutlinedButton.styleFrom(
                      minimumSize: const Size(0, 44),
                      padding: const EdgeInsets.symmetric(horizontal: 18),
                      shape: const StadiumBorder(),
                      foregroundColor: c.text,
                      side: BorderSide(color: c.border),
                      textStyle: const TextStyle(fontFamily: 'Figtree', fontWeight: FontWeight.w600),
                    ),
                    onPressed: _showCode,
                    child: Text(l.showQrOrPin),
                  ),
                ],
              ),
            ),
          ),
        ),
        const SizedBox(height: 16),
        Material(
          color: c.surface,
          shape: RoundedRectangleBorder(
            side: BorderSide(color: c.border),
            borderRadius: BorderRadius.circular(18),
          ),
          child: const Padding(padding: EdgeInsets.symmetric(vertical: 8), child: ReceiveSection()),
        ),
      ],
    );
  }
}
