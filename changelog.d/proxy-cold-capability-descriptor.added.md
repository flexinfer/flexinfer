`GET /v1/models/{name}` answers from the Model CR without waking the lane: the OpenAI model object
plus a `flexinfer` descriptor with the lane's phase, loading substage, ETA and reroute lane, the
paths it serves (`flexinfer.ai/serve-paths` or its backend's defaults), its input and output
modalities, `max_model_len`, and for diffusers lanes the image, video or audio limits the server
enforces for its `pipelineMode` with the defaults from `spec.config`. `GET /v1/models` carries the
same block on every Model. Callers that copied lane limits (imagegen-ui's Wan 2.1 bounds) or probe
readiness by sending a request can read this instead.
