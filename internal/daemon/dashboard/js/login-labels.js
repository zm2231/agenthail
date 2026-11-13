const sources = { api_key: "API key", oauth_token: "OAuth token", bedrock: "Bedrock", vertex: "Vertex AI", foundry: "Foundry", cloud_provider: "cloud provider" };

export function loginLabel(login, source) {
  if (login === "api_key") return sources[source] || sources.api_key;
  if (login === "signed_out") return "signed out";
  return null;
}
