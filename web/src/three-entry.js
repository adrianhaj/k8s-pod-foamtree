// Only what scene3d.jsx uses, so esbuild can drop the rest of three.js.
import {
  BoxGeometry, BufferGeometry, CanvasTexture, Color, Float32BufferAttribute, Group, InstancedMesh, Matrix4,
  MeshBasicMaterial, OrthographicCamera, PlaneGeometry, Raycaster, RepeatWrapping, Scene, Sprite, SpriteMaterial,
  SRGBColorSpace, Vector2, WebGLRenderer,
} from "three";
import { OrbitControls } from "../vendor/three/OrbitControls.js";

window.THREE = {
  BoxGeometry, BufferGeometry, CanvasTexture, Color, Float32BufferAttribute, Group, InstancedMesh, Matrix4,
  MeshBasicMaterial, OrthographicCamera, PlaneGeometry, Raycaster, RepeatWrapping, Scene, Sprite, SpriteMaterial,
  SRGBColorSpace, Vector2, WebGLRenderer, OrbitControls,
};
