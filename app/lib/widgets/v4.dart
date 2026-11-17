/// The small pieces « Version 4 » repeats on every screen (plan J8, WP-8.1): the square box of a
/// multiple choice, the badge of a direction (↗ sent, ↙ received).
library;

import 'package:flutter/material.dart';

import '../theme.dart';

/// The square box of a multiple choice, drawn alone (inside a tile, a row, a label).
class PickBox extends StatelessWidget {
  const PickBox({super.key, required this.on, this.size = 26, this.onImage = false});

  final bool on;
  final double size;

  /// Over a picture: a white edge and a dark fill, readable on any image.
  final bool onImage;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return AnimatedContainer(
      duration: MediaQuery.of(context).disableAnimations ? Duration.zero : const Duration(milliseconds: 120),
      width: size,
      height: size,
      decoration: BoxDecoration(
        color: on ? c.strong : (onImage ? Colors.black.withValues(alpha: 0.4) : Colors.transparent),
        borderRadius: BorderRadius.circular(size * 0.3),
        border: Border.all(color: on ? c.strong : (onImage ? Colors.white : c.muted), width: 2),
      ),
      child: on ? Icon(Icons.check_rounded, size: size * 0.62, color: c.onStrong) : null,
    );
  }
}

/// A label and its square box, as one button: « Tout sélectionner », « Tout ce jour ».
class BoxToggle extends StatelessWidget {
  const BoxToggle({super.key, required this.label, required this.on, required this.onPressed, this.semanticLabel, this.small = false});

  final String label;
  final bool on;
  final VoidCallback? onPressed;

  /// What a screen reader says, when the label alone is not enough.
  final String? semanticLabel;
  final bool small;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return Semantics(
      button: true,
      toggled: on,
      label: semanticLabel,
      excludeSemantics: semanticLabel != null,
      child: TextButton(
        onPressed: onPressed,
        style: TextButton.styleFrom(
          foregroundColor: small ? c.muted : c.text,
          padding: const EdgeInsets.symmetric(horizontal: 10),
          textStyle: TextStyle(fontFamily: fontFamily, fontWeight: FontWeight.w700, fontSize: small ? 13 : 14),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [Text(label), const SizedBox(width: 10), PickBox(on: on, size: small ? 24 : 26)],
        ),
      ),
    );
  }
}

/// The direction of a transfer: ↗ sent, ↙ received, on the strong color.
class DirectionBadge extends StatelessWidget {
  const DirectionBadge({super.key, required this.received, this.size = 44});

  final bool received;
  final double size;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(color: c.strong, borderRadius: BorderRadius.circular(12)),
      child: Icon(received ? Icons.south_west_rounded : Icons.north_east_rounded, color: c.onStrong, size: size / 2),
    );
  }
}

/// One choice among several filters (an album, a kind of file): outlined, the chosen one in
/// the text's color.
class ChoicePill extends StatelessWidget {
  const ChoicePill({super.key, required this.label, required this.on, required this.onPressed});

  final String label;
  final bool on;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    final c = context.colors;
    return Semantics(
      selected: on,
      child: OutlinedButton(
        onPressed: onPressed,
        style: OutlinedButton.styleFrom(
          minimumSize: const Size(0, 44),
          padding: const EdgeInsets.symmetric(horizontal: 14),
          foregroundColor: on ? c.text : c.muted,
          side: BorderSide(color: on ? c.text : c.border, width: 2),
          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
          textStyle: const TextStyle(fontFamily: fontFamily, fontWeight: FontWeight.w700, fontSize: 14),
        ),
        child: Text(label),
      ),
    );
  }
}
