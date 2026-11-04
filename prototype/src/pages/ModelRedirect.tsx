import { Navigate } from 'react-router-dom';
import { demoModels } from '../data/demo';

export function ModelRedirect() { return <Navigate to={`/modele/${demoModels[0].id}`} replace />; }
