import { defineRouteMiddleware } from "@astrojs/starlight/route-data";
import { langFromTag, ogImage } from "./lib/seo";

export const onRequest = defineRouteMiddleware((context) => {
  const route = context.locals.starlightRoute;
  const image = ogImage(langFromTag(route.lang), context.site!);
  route.head.push(
    { tag: "meta", attrs: { property: "og:image", content: image.url } },
    {
      tag: "meta",
      attrs: { property: "og:image:width", content: String(image.width) },
    },
    {
      tag: "meta",
      attrs: { property: "og:image:height", content: String(image.height) },
    },
    { tag: "meta", attrs: { property: "og:image:alt", content: image.alt } },
  );
  // 404 由 not_found_handling 在任意未命中路径下返回，其 canonical 指向并不存在的 /404/。
  if (route.id === "404") {
    route.head.push({
      tag: "meta",
      attrs: { name: "robots", content: "noindex" },
    });
  }
});
