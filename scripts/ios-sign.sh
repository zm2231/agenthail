#!/usr/bin/env bash
#   ios-sign.sh import
#   ios-sign.sh archive ARCHIVE VERSION BUILD
#   ios-sign.sh export ARCHIVE EXPORT_DIR
#   ios-sign.sh verify IPA
set -euo pipefail

: "${APPLE_TEAM_ID:?missing APPLE_TEAM_ID}"
: "${IOS_BUNDLE_ID:?missing AGENTHAIL_IOS_BUNDLE_ID repository variable}"
: "${RUNNER_TEMP:?missing RUNNER_TEMP}"

case "${1:-}" in
  import)
    : "${SIGNING_IDENTITIES_P12:?missing APPLE_IOS_SIGNING_IDENTITIES_P12_BASE64}"
    : "${SIGNING_IDENTITIES_P12_PASSWORD:?missing APPLE_IOS_SIGNING_IDENTITIES_P12_PASSWORD}"
    : "${PROVISIONING_PROFILE:?missing APPLE_IOS_PROVISIONING_PROFILE_BASE64}"
    : "${NOTIFICATION_SERVICE_PROVISIONING_PROFILE:?missing APPLE_IOS_NOTIFICATION_SERVICE_PROVISIONING_PROFILE_BASE64}"
    : "${ASC_KEY:?missing APPLE_NOTARY_KEY_BASE64}"
    : "${ASC_KEY_ID:?missing APPLE_NOTARY_KEY_ID}"
    umask 077
    KEYCHAIN_PATH="$RUNNER_TEMP/agenthail-ios-signing.keychain-db"
    KEYCHAIN_PASSWORD="$(openssl rand -hex 24)"
    printf '%s' "$SIGNING_IDENTITIES_P12" | base64 --decode > "$RUNNER_TEMP/SigningIdentities.p12"
    security create-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN_PATH"
    security set-keychain-settings -lut 21600 "$KEYCHAIN_PATH"
    security unlock-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN_PATH"
    security import "$RUNNER_TEMP/SigningIdentities.p12" -k "$KEYCHAIN_PATH" -P "$SIGNING_IDENTITIES_P12_PASSWORD" -T /usr/bin/codesign
    security set-key-partition-list -S apple-tool:,apple: -s -k "$KEYCHAIN_PASSWORD" "$KEYCHAIN_PATH"
    security list-keychains -d user -s "$KEYCHAIN_PATH"
    security default-keychain -d user -s "$KEYCHAIN_PATH"
    SIGNING_IDENTITY="$(security find-identity -v -p codesigning "$KEYCHAIN_PATH" | sed -nE "s/.*\"(Apple Distribution:.*\\(${APPLE_TEAM_ID}\\))\".*/\\1/p" | head -n 1)"
    test -n "$SIGNING_IDENTITY"
    DEVELOPMENT_IDENTITY=""
    while IFS='|' read -r _ identity_name; do
      if security find-certificate -c "$identity_name" -p "$KEYCHAIN_PATH" \
        | openssl x509 -noout -subject -nameopt RFC2253 \
        | grep -Fq "OU=$APPLE_TEAM_ID"; then
        DEVELOPMENT_IDENTITY="$identity_name"
        break
      fi
    done < <(security find-identity -v -p codesigning "$KEYCHAIN_PATH" | sed -nE 's/^[[:space:]]*[0-9]+\) ([0-9A-F]+) "(Apple Development:[^"]+)"/\1|\2/p')
    test -n "$DEVELOPMENT_IDENTITY"
    SIGNING_CERT_SHA="$(security find-certificate -c "$SIGNING_IDENTITY" -p "$KEYCHAIN_PATH" | openssl x509 -outform DER | shasum -a 256 | awk '{print $1}')"
    mkdir -p "$HOME/Library/Developer/Xcode/UserData/Provisioning Profiles" "$HOME/Library/MobileDevice/Provisioning Profiles"
    install_profile() {
      local encoded="$1" name="$2" bundle="$3" profile plist uuid index cert_found
      profile="$RUNNER_TEMP/$name.mobileprovision"
      plist="$RUNNER_TEMP/$name-profile.plist"
      printf '%s' "$encoded" | base64 --decode > "$profile"
      security cms -D -i "$profile" -o "$plist"
      uuid="$(plutil -extract UUID raw -o - "$plist")"
      test -n "$(plutil -extract Name raw -o - "$plist")"
      test "$(plutil -extract TeamIdentifier.0 raw -o - "$plist")" = "$APPLE_TEAM_ID"
      test "$(plutil -extract Entitlements.application-identifier raw -o - "$plist")" = "$APPLE_TEAM_ID.$bundle"
      test "$(plutil -extract Entitlements.get-task-allow raw -o - "$plist")" = "false"
      if [[ "$(plutil -extract IsXcodeManaged raw -o - "$plist" 2>/dev/null || echo false)" == true ]]; then
        echo "$name profile is managed by Xcode; export needs a manually created App Store profile" >&2
        exit 1
      fi
      if plutil -extract ProvisionedDevices raw -o - "$plist" >/dev/null 2>&1; then
        echo "$name profile lists devices; an App Store profile is required" >&2
        exit 1
      fi
      plutil -extract Entitlements.keychain-access-groups json -o - "$plist" \
        | grep -Fq -e "\"$APPLE_TEAM_ID.*\"" -e "\"$APPLE_TEAM_ID.$IOS_BUNDLE_ID.shared\""
      [[ "$(plutil -extract ExpirationDate raw -o - "$plist")" > "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" ]]
      cert_found=false
      for index in $(seq 0 $(( $(plutil -extract DeveloperCertificates raw -o - "$plist") - 1 ))); do
        if [[ "$(plutil -extract "DeveloperCertificates.$index" raw -o - "$plist" | base64 --decode | shasum -a 256 | awk '{print $1}')" == "$SIGNING_CERT_SHA" ]]; then
          cert_found=true
        fi
      done
      if [[ "$cert_found" != true ]]; then
        echo "$name profile does not include the Apple Distribution certificate" >&2
        exit 1
      fi
      cp "$profile" "$HOME/Library/Developer/Xcode/UserData/Provisioning Profiles/$uuid.mobileprovision"
      cp "$profile" "$HOME/Library/MobileDevice/Provisioning Profiles/$uuid.mobileprovision"
      printf '%s\n' "$uuid" > "$RUNNER_TEMP/$name.profile-uuid"
    }
    install_profile "$PROVISIONING_PROFILE" Agenthail "$IOS_BUNDLE_ID"
    install_profile "$NOTIFICATION_SERVICE_PROVISIONING_PROFILE" AgenthailNotificationService "$IOS_BUNDLE_ID.NotificationService"
    mkdir -p "$HOME/.appstoreconnect/private_keys"
    printf '%s' "$ASC_KEY" | base64 --decode > "$HOME/.appstoreconnect/private_keys/AuthKey_${ASC_KEY_ID}.p8"
    chmod 600 "$HOME/.appstoreconnect/private_keys/AuthKey_${ASC_KEY_ID}.p8"
    ;;
  archive)
    : "${ASC_KEY_ID:?missing APPLE_NOTARY_KEY_ID}"
    : "${ASC_ISSUER_ID:?missing APPLE_NOTARY_ISSUER_ID}"
    archive="$2" version="$3" build="$4"
    test "$(xcrun --sdk iphoneos --show-sdk-version | cut -d. -f1)" -ge 26
    xcodebuild archive \
      -project native/Agenthail.xcodeproj \
      -scheme AgenthailIOS \
      -configuration Release \
      -destination 'generic/platform=iOS' \
      -archivePath "$archive" \
      -allowProvisioningUpdates \
      -authenticationKeyPath "$HOME/.appstoreconnect/private_keys/AuthKey_${ASC_KEY_ID}.p8" \
      -authenticationKeyID "$ASC_KEY_ID" \
      -authenticationKeyIssuerID "$ASC_ISSUER_ID" \
      DEVELOPMENT_TEAM="$APPLE_TEAM_ID" \
      AGENTHAIL_IOS_BUNDLE_ID="$IOS_BUNDLE_ID" \
      CODE_SIGN_STYLE=Automatic \
      MARKETING_VERSION="$version" \
      CURRENT_PROJECT_VERSION="$build"
    ;;
  export)
    archive="$2" export_dir="$3"
    app_profile="$(cat "$RUNNER_TEMP/Agenthail.profile-uuid")"
    service_profile="$(cat "$RUNNER_TEMP/AgenthailNotificationService.profile-uuid")"
    cat > "$RUNNER_TEMP/ExportOptions.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>method</key><string>app-store-connect</string>
  <key>signingStyle</key><string>manual</string>
  <key>signingCertificate</key><string>Apple Distribution</string>
  <key>teamID</key><string>${APPLE_TEAM_ID}</string>
  <key>provisioningProfiles</key><dict>
    <key>${IOS_BUNDLE_ID}</key><string>${app_profile}</string>
    <key>${IOS_BUNDLE_ID}.NotificationService</key><string>${service_profile}</string>
  </dict>
</dict></plist>
PLIST
    xcodebuild -exportArchive \
      -archivePath "$archive" \
      -exportPath "$export_dir" \
      -exportOptionsPlist "$RUNNER_TEMP/ExportOptions.plist"
    ;;
  verify)
    ipa="$2"
    inspect="$RUNNER_TEMP/ipa-inspect"
    rm -rf "$inspect"
    mkdir -p "$inspect"
    unzip -q "$ipa" -d "$inspect"
    app="$inspect/Payload/Agenthail.app"
    service="$app/PlugIns/AgenthailNotificationService.appex"
    shared="$APPLE_TEAM_ID.$IOS_BUNDLE_ID.shared"
    test "$(plutil -extract CFBundleIdentifier raw -o - "$app/Info.plist")" = "$IOS_BUNDLE_ID"
    test "$(plutil -extract CFBundleIdentifier raw -o - "$service/Info.plist")" = "$IOS_BUNDLE_ID.NotificationService"
    test "$(plutil -extract AgenthailKeychainGroup raw -o - "$app/Info.plist")" = "$shared"
    test "$(plutil -extract AgenthailKeychainGroup raw -o - "$service/Info.plist")" = "$shared"
    codesign --verify --deep --strict "$app"
    codesign -d --entitlements :- "$app" > "$RUNNER_TEMP/ios-entitlements.plist" 2>/dev/null
    codesign -d --entitlements :- "$service" > "$RUNNER_TEMP/ios-service-entitlements.plist" 2>/dev/null
    entitlement() { /usr/libexec/PlistBuddy -c "Print :$2" "$1"; }
    test "$(entitlement "$RUNNER_TEMP/ios-entitlements.plist" aps-environment)" = "production"
    test "$(entitlement "$RUNNER_TEMP/ios-entitlements.plist" com.apple.developer.devicecheck.appattest-environment)" = "production"
    test "$(entitlement "$RUNNER_TEMP/ios-entitlements.plist" get-task-allow)" = "false"
    test "$(entitlement "$RUNNER_TEMP/ios-entitlements.plist" keychain-access-groups:0)" = "$APPLE_TEAM_ID.$IOS_BUNDLE_ID"
    test "$(entitlement "$RUNNER_TEMP/ios-entitlements.plist" keychain-access-groups:1)" = "$shared"
    test "$(entitlement "$RUNNER_TEMP/ios-service-entitlements.plist" get-task-allow)" = "false"
    test "$(entitlement "$RUNNER_TEMP/ios-service-entitlements.plist" keychain-access-groups:0)" = "$shared"
    if entitlement "$RUNNER_TEMP/ios-service-entitlements.plist" keychain-access-groups:1 >/dev/null 2>&1; then
      echo "the notification service has a keychain group beyond the shared one" >&2
      exit 1
    fi
    ;;
  *)
    echo "usage: $0 import | archive ARCHIVE VERSION BUILD | export ARCHIVE EXPORT_DIR | verify IPA" >&2
    exit 2
    ;;
esac
