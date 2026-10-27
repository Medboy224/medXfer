/// Recevoir, on a desktop (no mockup of its own): where received files go. The connection is
/// not this page's: this computer is seen by its network while the window is open, and the
/// offer of a device connected opens over any page (decided on 27 October 2026).
library;

import 'package:flutter/material.dart';

import '../../l10n/app_localizations.dart';
import '../../theme.dart';
import '../settings/settings_screen.dart';

class ReceivePage extends StatelessWidget {
  const ReceivePage({super.key});

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    final c = context.colors;
    return ListView(
      padding: const EdgeInsets.all(24),
      children: [
        Text(l.navReceive, style: const TextStyle(fontSize: 24, fontWeight: FontWeight.w800)),
        const SizedBox(height: 8),
        Text(l.receiveHint, style: TextStyle(color: c.muted, height: 1.4)),
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
