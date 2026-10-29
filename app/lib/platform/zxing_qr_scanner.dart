/// flutter_zxing's adapter of [QrScanner] (zxing-cpp, free; decision J5): a camera screen that
/// closes on the first medXfer link it reads. Another library would replace this file alone.
library;

import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_zxing/flutter_zxing.dart';

import '../l10n/app_localizations.dart';
import 'qr_scanner.dart';

class ZxingQrScanner implements QrScanner {
  const ZxingQrScanner();

  @override
  bool get supported => Platform.isAndroid;

  @override
  Future<String?> scan(BuildContext context) =>
      Navigator.of(context).push<String>(MaterialPageRoute(builder: (_) => const _ScanScreen()));
}

QrScanner systemQrScanner() => Platform.isAndroid ? const ZxingQrScanner() : const NoQrScanner();

class _ScanScreen extends StatefulWidget {
  const _ScanScreen();

  @override
  State<_ScanScreen> createState() => _ScanScreenState();
}

class _ScanScreenState extends State<_ScanScreen> {
  bool _done = false;

  void _read(Code code) {
    final text = code.text?.trim() ?? '';
    // Another QR code (a menu, a Wi-Fi of the system) is not ours: keep looking.
    if (_done || !code.isValid || !text.startsWith('medxfer://')) return;
    _done = true;
    Navigator.of(context).pop(text);
  }

  @override
  Widget build(BuildContext context) {
    final l = AppLocalizations.of(context);
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        backgroundColor: Colors.black,
        foregroundColor: Colors.white,
        title: Text(l.scanQr),
      ),
      body: Stack(
        children: [
          ReaderWidget(
            codeFormat: Format.qrCode,
            onScan: _read,
            showGallery: false,
            showToggleCamera: false,
            tryHarder: true,
            scanDelay: const Duration(milliseconds: 200),
            scanDelaySuccess: const Duration(milliseconds: 200),
            cropPercent: 0.7,
            // 1080p rather than 720p: a QR code on a computer's screen, from where the camera
            // focuses, is small in the picture (real test of 29 October 2026, 24 s to read).
            resolution: ResolutionPreset.veryHigh,
          ),
          Positioned(
            left: 24,
            right: 24,
            top: 16,
            child: Text(
              l.scanHint,
              key: const Key('scan-hint'),
              textAlign: TextAlign.center,
              style: const TextStyle(color: Colors.white, fontSize: 14, height: 1.4),
            ),
          ),
        ],
      ),
    );
  }
}
