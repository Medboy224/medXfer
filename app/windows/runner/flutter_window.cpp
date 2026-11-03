#include "flutter_window.h"

#include <flutter/standard_method_codec.h>

#include <memory>
#include <optional>
#include <string>

#include "flutter/generated_plugin_registrant.h"

FlutterWindow::FlutterWindow(const flutter::DartProject& project)
    : project_(project) {}

FlutterWindow::~FlutterWindow() {}

bool FlutterWindow::OnCreate() {
  if (!Win32Window::OnCreate()) {
    return false;
  }

  RECT frame = GetClientArea();

  // The size here must match the window dimensions to avoid unnecessary surface
  // creation / destruction in the startup path.
  flutter_controller_ = std::make_unique<flutter::FlutterViewController>(
      frame.right - frame.left, frame.bottom - frame.top, project_);
  // Ensure that basic setup of the controller was successful.
  if (!flutter_controller_->engine() || !flutter_controller_->view()) {
    return false;
  }
  RegisterPlugins(flutter_controller_->engine());
  hotspot_channel_ = std::make_unique<flutter::MethodChannel<flutter::EncodableValue>>(
      flutter_controller_->engine()->messenger(), "dev.medxfer.app/hotspot",
      &flutter::StandardMethodCodec::GetInstance());
  hotspot_channel_->SetMethodCallHandler([this](const auto& call, auto result) {
    if (call.method_name() == "start") {
      const auto* args = std::get_if<flutter::EncodableMap>(call.arguments());
      auto text = [args](const char* key) -> std::string {
        if (!args) return "";
        auto it = args->find(flutter::EncodableValue(key));
        if (it == args->end()) return "";
        const auto* s = std::get_if<std::string>(&it->second);
        return s ? *s : "";
      };
      // Answered once the group runs, the window drawing meanwhile.
      std::shared_ptr<flutter::MethodResult<flutter::EncodableValue>> reply = std::move(result);
      hotspot_.Start(text("ssid"), text("passphrase"), [reply](std::optional<std::string> error, HotspotInterface group) {
        if (error) {
          reply->Error("hotspot", *error);
          return;
        }
        reply->Success(flutter::EncodableValue(flutter::EncodableMap{
            {flutter::EncodableValue("name"), flutter::EncodableValue(group.name)},
            {flutter::EncodableValue("index"), flutter::EncodableValue(static_cast<int32_t>(group.index))},
            {flutter::EncodableValue("address"), flutter::EncodableValue(group.address)},
            {flutter::EncodableValue("prefix"), flutter::EncodableValue(static_cast<int32_t>(group.prefix))},
            {flutter::EncodableValue("windowsDhcp"), flutter::EncodableValue(group.windows_dhcp)},
            {flutter::EncodableValue("heldBy"), flutter::EncodableValue(group.held_by)},
            {flutter::EncodableValue("sharingAddress"), flutter::EncodableValue(group.sharing_address)},
        }));
      });
    } else if (call.method_name() == "stop") {
      hotspot_.Stop();
      result->Success();
    } else if (call.method_name() == "join") {
      const auto* args = std::get_if<flutter::EncodableMap>(call.arguments());
      auto text = [args](const char* key) -> std::string {
        if (!args) return "";
        auto it = args->find(flutter::EncodableValue(key));
        if (it == args->end()) return "";
        const auto* s = std::get_if<std::string>(&it->second);
        return s ? *s : "";
      };
      int security = 1;
      if (args) {
        auto it = args->find(flutter::EncodableValue("security"));
        if (it != args->end()) {
          if (const auto* n = std::get_if<int32_t>(&it->second)) security = *n;
        }
      }
      auto band = text("band");
      std::string ssid = text("ssid");
      std::shared_ptr<flutter::MethodResult<flutter::EncodableValue>> reply = std::move(result);
      wifi_join_.Join(ssid, text("passphrase"), security, band.empty() ? 0 : band[0],
                      [reply, ssid](std::optional<std::string> error, JoinedNetwork joined) {
        if (error) {
          reply->Error(*error, *error);
          return;
        }
        reply->Success(flutter::EncodableValue(flutter::EncodableMap{
            {flutter::EncodableValue("ssid"), flutter::EncodableValue(ssid)},
            {flutter::EncodableValue("name"), flutter::EncodableValue(joined.name)},
            {flutter::EncodableValue("index"), flutter::EncodableValue(static_cast<int32_t>(joined.index))},
            {flutter::EncodableValue("address"), flutter::EncodableValue(joined.address)},
            {flutter::EncodableValue("prefix"), flutter::EncodableValue(static_cast<int32_t>(joined.prefix))},
            {flutter::EncodableValue("frequency"), flutter::EncodableValue(static_cast<int32_t>(joined.frequency))},
        }));
      });
    } else if (call.method_name() == "leave") {
      wifi_join_.Leave();
      result->Success();
    } else if (call.method_name() == "alive") {
      result->Success(flutter::EncodableValue(hotspot_.Alive()));
    } else if (call.method_name() == "wifiOn" || call.method_name() == "turnWifiOn") {
      std::shared_ptr<flutter::MethodResult<flutter::EncodableValue>> reply = std::move(result);
      auto answer = [reply](bool on) { reply->Success(flutter::EncodableValue(on)); };
      if (call.method_name() == "wifiOn") {
        WifiRadioOn(answer);
      } else {
        TurnWifiOn(answer);
      }
    } else {
      result->NotImplemented();
    }
  });
  // Windows' firewall (lib/platform/firewall.dart): the networks it keeps this program out of,
  // and the user's permission to let it in.
  firewall_channel_ = std::make_unique<flutter::MethodChannel<flutter::EncodableValue>>(
      flutter_controller_->engine()->messenger(), "dev.medxfer.app/firewall",
      &flutter::StandardMethodCodec::GetInstance());
  firewall_channel_->SetMethodCallHandler([](const auto& call, auto result) {
    if (call.method_name() == "status") {
      flutter::EncodableList networks;
      for (const auto& n : FirewallStatus()) {
        networks.push_back(flutter::EncodableValue(flutter::EncodableMap{
            {flutter::EncodableValue("name"), flutter::EncodableValue(n.name)},
            {flutter::EncodableValue("category"), flutter::EncodableValue(n.category)},
            {flutter::EncodableValue("allowed"), flutter::EncodableValue(n.allowed)},
        }));
      }
      result->Success(flutter::EncodableValue(networks));
    } else if (call.method_name() == "allow") {
      result->Success(flutter::EncodableValue(FirewallAllow()));
    } else {
      result->NotImplemented();
    }
  });
  // The wired cards (lib/platform/cable.dart, WindowsCableLink): their addresses set by hand.
  network_channel_ = std::make_unique<flutter::MethodChannel<flutter::EncodableValue>>(
      flutter_controller_->engine()->messenger(), "dev.medxfer.app/network",
      &flutter::StandardMethodCodec::GetInstance());
  network_channel_->SetMethodCallHandler([](const auto& call, auto result) {
    if (call.method_name() == "manualWired") {
      flutter::EncodableList addresses;
      for (const auto& a : ManualWiredAddresses()) {
        addresses.push_back(flutter::EncodableValue(flutter::EncodableMap{
            {flutter::EncodableValue("card"), flutter::EncodableValue(a.card)},
            {flutter::EncodableValue("address"), flutter::EncodableValue(a.address)},
        }));
      }
      result->Success(flutter::EncodableValue(addresses));
    } else {
      result->NotImplemented();
    }
  });
  // Bluetooth Low Energy (lib/platform/ble.dart, ChannelBle): the same channel as Android's Ble.kt.
  ble_channel_ = std::make_unique<flutter::MethodChannel<flutter::EncodableValue>>(
      flutter_controller_->engine()->messenger(), "dev.medxfer.app/ble",
      &flutter::StandardMethodCodec::GetInstance());
  ble_ = std::make_unique<Ble>([this](const std::string& event, flutter::EncodableMap args) {
    if (!ble_channel_) return;
    args[flutter::EncodableValue("event")] = flutter::EncodableValue(event);
    ble_channel_->InvokeMethod("event", std::make_unique<flutter::EncodableValue>(std::move(args)));
  });
  ble_channel_->SetMethodCallHandler([this](const auto& call, auto result) {
    const auto* args = std::get_if<flutter::EncodableMap>(call.arguments());
    auto arg = [args](const char* key) -> const flutter::EncodableValue* {
      if (!args) return nullptr;
      auto it = args->find(flutter::EncodableValue(key));
      return it == args->end() ? nullptr : &it->second;
    };
    auto text = [&arg](const char* key) -> std::string {
      const auto* v = arg(key);
      const auto* s = v ? std::get_if<std::string>(v) : nullptr;
      return s ? *s : "";
    };
    auto bytes = [&arg](const char* key) -> std::vector<uint8_t> {
      const auto* v = arg(key);
      const auto* b = v ? std::get_if<std::vector<uint8_t>>(v) : nullptr;
      return b ? *b : std::vector<uint8_t>{};
    };
    auto flag = [&arg](const char* key) {
      const auto* v = arg(key);
      const auto* b = v ? std::get_if<bool>(v) : nullptr;
      return b ? *b : true;
    };
    const auto& m = call.method_name();
    std::shared_ptr<flutter::MethodResult<flutter::EncodableValue>> reply = std::move(result);
    if (m == "enabled" || m == "turnOn") {
      auto answer = [reply](bool on) { reply->Success(flutter::EncodableValue(on)); };
      if (m == "enabled") {
        ble_->Enabled(answer);
      } else {
        ble_->TurnOn(answer);
      }
    } else if (m == "permission") {
      reply->Success(flutter::EncodableValue(true));  // a desktop program asks none
    } else if (m == "start") {
      ble_->Start(flag("advertise"), flag("scan"), [reply](std::optional<std::string> error) {
        if (error) {
          reply->Error(*error, *error);
        } else {
          reply->Success();
        }
      });
    } else if (m == "stop") {
      ble_->Stop();
      reply->Success();
    } else if (m == "reply") {
      ble_->Reply(text("conn"), bytes("data"));
      reply->Success();
    } else if (m == "exchange") {
      ble_->Exchange(text("device"), bytes("data"), [reply](std::optional<std::string> error, std::vector<uint8_t> answer) {
        if (error) {
          reply->Error("exchange", *error);
        } else {
          reply->Success(flutter::EncodableValue(std::move(answer)));
        }
      });
    } else if (m == "disconnect") {
      ble_->Disconnect(text("device"));
      reply->Success();
    } else if (m == "bands") {
      auto b = OwnWifiBands();
      reply->Success(flutter::EncodableValue(flutter::EncodableMap{
          {flutter::EncodableValue("create"), flutter::EncodableValue(b.create)},
          {flutter::EncodableValue("join"), flutter::EncodableValue(b.join)},
      }));
    } else {
      reply->NotImplemented();
    }
  });
  SetChildContent(flutter_controller_->view()->GetNativeWindow());

  flutter_controller_->engine()->SetNextFrameCallback([&]() {
    this->Show();
  });

  // Flutter can complete the first frame before the "show window" callback is
  // registered. The following call ensures a frame is pending to ensure the
  // window is shown. It is a no-op if the first frame hasn't completed yet.
  flutter_controller_->ForceRedraw();

  return true;
}

void FlutterWindow::OnDestroy() {
  hotspot_.Stop();
  wifi_join_.Leave();
  ble_ = nullptr;
  ble_channel_ = nullptr;
  hotspot_channel_ = nullptr;
  firewall_channel_ = nullptr;
  network_channel_ = nullptr;
  if (flutter_controller_) {
    flutter_controller_ = nullptr;
  }

  Win32Window::OnDestroy();
}

LRESULT
FlutterWindow::MessageHandler(HWND hwnd, UINT const message,
                              WPARAM const wparam,
                              LPARAM const lparam) noexcept {
  // Give Flutter, including plugins, an opportunity to handle window messages.
  if (flutter_controller_) {
    std::optional<LRESULT> result =
        flutter_controller_->HandleTopLevelWindowProc(hwnd, message, wparam,
                                                      lparam);
    if (result) {
      return *result;
    }
  }

  switch (message) {
    case WM_FONTCHANGE:
      flutter_controller_->engine()->ReloadSystemFonts();
      break;
  }

  return Win32Window::MessageHandler(hwnd, message, wparam, lparam);
}
