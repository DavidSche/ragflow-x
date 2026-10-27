/**
 * CitationImage – fetches a referenced chunk image (authorized) and renders it.
 */
import { useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { api } from "../../lib/api";

export function CitationImage({
  imageId,
  datasetId,
  docId,
  chunkId,
  alt,
  className,
}: {
  imageId: string;
  datasetId?: string;
  docId?: string;
  chunkId?: string;
  alt?: string;
  className?: string;
}) {
  const [url, setUrl] = useState<string>();
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let cancelled = false;
    let objectUrl: string | undefined;
    setUrl(undefined);
    setFailed(false);
    (async () => {
      try {
        const params = new URLSearchParams({ image: imageId });
        if (datasetId) params.set("dataset", datasetId);
        if (docId) params.set("doc", docId);
        if (chunkId) params.set("chunk", chunkId);
        const res = await api.get(`/chat/image?${params.toString()}`, {
          responseType: "blob",
        });
        objectUrl = URL.createObjectURL(res.data as Blob);
        if (!cancelled) setUrl(objectUrl);
      } catch {
        if (!cancelled) setFailed(true);
      }
    })();
    return () => {
      cancelled = true;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [imageId, datasetId, docId, chunkId]);

  if (failed) return null;
  if (!url) return <Loader2 className="size-4 animate-spin text-muted-foreground" />;
  return <img src={url} alt={alt ?? ""} className={className} />;
}
