using Newtonsoft.Json;
using Newtonsoft.Json.Linq;

namespace BibiControl;

// Wire-compatible mirror of github.com/asemones/bibicontrol/ipc.Envelope.
//
// Messages are newline-delimited JSON: each envelope is a compact JSON object
// terminated by '\n' (matching Go's json.Encoder/Decoder). The control plane is
// the TCP client and sends requests; this DLL is the server and sends responses.
//
// Routing rule (must match the Go side): a response sets ReplyTo to the incoming
// request's Id. Success carries Payload; failure sets Error (and Kind "error").
public sealed class Envelope
{
	[JsonProperty("id")]
	public string Id;

	[JsonProperty("reply_to")]
	public string ReplyTo;

	[JsonProperty("kind")]
	public string Kind;

	[JsonProperty("command")]
	public string Command;

	[JsonProperty("payload")]
	public JToken Payload;

	[JsonProperty("error")]
	public string Error;

	[JsonProperty("time")]
	public string Time;
}
