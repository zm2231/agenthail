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
    : "${ASC_KEY:?missing APPLE_NOTARY_KEY_BASE64}"
    : "${ASC_KEY_ID:?missing APPLE_NOTARY_KEY_ID}"
    umask 077
    KEYCHAIN_PATH="$RUNNER_TEMP/agenthail-ios-signing.keychain-db"
    KEYCHAIN_PASSWORD="$(openssl rand -hex 24)"
    printf '%s' "$SIGNING_IDENTITIES_P12" | base64 --decode > "$RUNNER_TEMP/SigningIdentities.p12"
    printf '%s' "$PROVISIONING_PROFILE" | base64 --decode > "$RUNNER_TEMP/Agenthail.mobileprovision"
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
    security cms -D -i "$RUNNER_TEMP/Agenthail.mobileprovision" -o "$RUNNER_TEMP/Agenthail-profile.plist"
    PROFILE_UUID="$(plutil -extract UUID raw -o - "$RUNNER_TEMP/Agenthail-profile.plist")"
    PROFILE_NAME="$(plutil -extract Name raw -o - "$RUNNER_TEMP/Agenthail-profile.plist")"
    PROFILE_EXPIRATION="$(plutil -extract ExpirationDate raw -o - "$RUNNER_TEMP/Agenthail-profile.plist")"
    PROFILE_CERT_SHA="$(plutil -extract DeveloperCertificates.0 raw -o - "$RUNNER_TEMP/Agenthail-profile.plist" | base64 --decode | shasum -a 256 | awk '{print $1}')"
    SIGNING_CERT_SHA="$(security find-certificate -c "$SIGNING_IDENTITY" -p "$KEYCHAIN_PATH" | openssl x509 -outform DER | shasum -a 256 | awk '{print $1}')"
    test "$(plutil -extract TeamIdentifier.0 raw -o - "$RUNNER_TEMP/Agenthail-profile.plist")" = "$APPLE_TEAM_ID"
    test "$(plutil -extract Entitlements.application-identifier raw -o - "$RUNNER_TEMP/Agenthail-profile.plist")" = "$APPLE_TEAM_ID.$IOS_BUNDLE_ID"
    test "$PROFILE_CERT_SHA" = "$SIGNING_CERT_SHA"
    [[ "$PROFILE_EXPIRATION" > "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" ]]
    mkdir -p "$HOME/Library/Developer/Xcode/UserData/Provisioning Profiles"
    mkdir -p "$HOME/Library/MobileDevice/Provisioning Profiles"
    cp "$RUNNER_TEMP/Agenthail.mobileprovision" "$HOME/Library/Developer/Xcode/UserData/Provisioning Profiles/$PROFILE_UUID.mobileprovision"
    cp "$RUNNER_TEMP/Agenthail.mobileprovision" "$HOME/Library/MobileDevice/Provisioning Profiles/$PROFILE_UUID.mobileprovision"
    test -n "$PROFILE_NAME"
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
    : "${ASC_KEY_ID:?missing APPLE_NOTARY_KEY_ID}"
    : "${ASC_ISSUER_ID:?missing APPLE_NOTARY_ISSUER_ID}"
    archive="$2" export_dir="$3"
    cat > "$RUNNER_TEMP/ExportOptions.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>method</key><string>app-store-connect</string>
  <key>signingStyle</key><string>automatic</string>
  <key>teamID</key><string>${APPLE_TEAM_ID}</string>
</dict></plist>
PLIST
    xcodebuild -exportArchive \
      -archivePath "$archive" \
      -exportPath "$export_dir" \
      -exportOptionsPlist "$RUNNER_TEMP/ExportOptions.plist" \
      -allowProvisioningUpdates \
      -authenticationKeyPath "$HOME/.appstoreconnect/private_keys/AuthKey_${ASC_KEY_ID}.p8" \
      -authenticationKeyID "$ASC_KEY_ID" \
      -authenticationKeyIssuerID "$ASC_ISSUER_ID"
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
    ! entitlement "$RUNNER_TEMP/ios-service-entitlements.plist" keychain-access-groups:1 >/dev/null 2>&1
    ;;
  *)
    echo "usage: $0 import | archive ARCHIVE VERSION BUILD | export ARCHIVE EXPORT_DIR | verify IPA" >&2
    exit 2
    ;;
esac
