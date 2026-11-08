#ifndef RUNNER_FLUTTER_WINDOW_H_
#define RUNNER_FLUTTER_WINDOW_H_

#include <flutter/dart_project.h>
#include <flutter/encodable_value.h>
#include <flutter/flutter_view_controller.h>
#include <flutter/method_channel.h>

#include <memory>

#include "ble.h"
#include "firewall.h"
#include "hotspot.h"
#include "win32_window.h"
#include "wifi_join.h"
#include "wired.h"

// A window that does nothing but host a Flutter view.
class FlutterWindow : public Win32Window {
 public:
  // Creates a new FlutterWindow hosting a Flutter view running |project|.
  explicit FlutterWindow(const flutter::DartProject& project);
  virtual ~FlutterWindow();

 protected:
  // Win32Window:
  bool OnCreate() override;
  void OnDestroy() override;
  LRESULT MessageHandler(HWND window, UINT const message, WPARAM const wparam,
                         LPARAM const lparam) noexcept override;

 private:
  // The project to run.
  flutter::DartProject project_;

  // The Flutter instance hosted by this window.
  std::unique_ptr<flutter::FlutterViewController> flutter_controller_;

  // The network this computer creates (lib/platform/hotspot.dart, WindowsHotspot).
  WifiDirectHotspot hotspot_;
  // Another device's network, joined by this computer (WindowsHotspot.join).
  WifiJoin wifi_join_;
  std::unique_ptr<flutter::MethodChannel<flutter::EncodableValue>> hotspot_channel_;

  // Windows' firewall (lib/platform/firewall.dart, WindowsFirewall).
  std::unique_ptr<flutter::MethodChannel<flutter::EncodableValue>> firewall_channel_;

  // The wired cards' addresses set by hand (lib/platform/cable.dart, WindowsCableLink).
  std::unique_ptr<flutter::MethodChannel<flutter::EncodableValue>> network_channel_;

  // The computer kept from sleeping while a transfer runs (lib/platform/awake.dart, #256).
  std::unique_ptr<flutter::MethodChannel<flutter::EncodableValue>> power_channel_;

  // Bluetooth Low Energy, for the path by BLE (lib/platform/ble.dart, ChannelBle).
  std::unique_ptr<Ble> ble_;
  std::unique_ptr<flutter::MethodChannel<flutter::EncodableValue>> ble_channel_;
};

#endif  // RUNNER_FLUTTER_WINDOW_H_
